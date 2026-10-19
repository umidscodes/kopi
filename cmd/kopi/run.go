package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	gosync "sync"
	"sync/atomic"
	"time"

	"fyne.io/systray"

	"github.com/umidscodes/kopi/internal/app"
	"github.com/umidscodes/kopi/internal/autostart"
	"github.com/umidscodes/kopi/internal/clip"
	"github.com/umidscodes/kopi/internal/config"
	"github.com/umidscodes/kopi/internal/icon"
	"github.com/umidscodes/kopi/internal/proto"
	ksync "github.com/umidscodes/kopi/internal/sync"
	"github.com/umidscodes/kopi/internal/transport"
)

const (
	// maxPeerSlots is how many devices the tray lists (menus can't grow cheaply).
	maxPeerSlots = 8
	// noPeersHintAfter is how long Kopi waits before suggesting what to check (DISC-7).
	noPeersHintAfter = 30 * time.Second
)

type kopiApp struct {
	cfgPath string

	mu       gosync.Mutex
	cfg      config.Config
	cfgMtime time.Time
	node     *app.Node
	started  time.Time
	startErr error
	writeErr error // can't write the log/config folder (e.g. blocked by antivirus)
	// translocated: macOS runs a quarantined download from a temporary copy; start at
	// login and permissions get confused until the quarantine flag is cleared.
	translocated bool
	flash        string // short-lived status override ("Clip too large…")
	flashTil     time.Time

	ctx        context.Context
	cancel     context.CancelFunc
	dirty      chan struct{}
	dialogOpen atomic.Bool

	mStatus    *systray.MenuItem
	mDevices   *systray.MenuItem
	mPeers     [maxPeerSlots]*systray.MenuItem
	mPause     *systray.MenuItem
	mSensitive *systray.MenuItem
	mPass      *systray.MenuItem
	mLogin     *systray.MenuItem
	mQuit      *systray.MenuItem
}

func runApp() {
	path, err := config.Path()
	if err != nil {
		showMessage("Kopi can't find your config folder: " + err.Error())
		os.Exit(1)
	}
	logErr := setupLog(filepath.Dir(path))
	a := &kopiApp{cfgPath: path, dirty: make(chan struct{}, 1), writeErr: logErr}
	if exe, err := autostart.Executable(); err == nil && autostart.Translocated(exe) {
		a.translocated = true
		log.Print("running from App Translocation (quarantined download)")
	}
	a.ctx, a.cancel = context.WithCancel(context.Background())
	systray.Run(a.onReady, a.onExit)
}

// setupLog writes to kopi.log next to the config. Never logs clip text or passphrases.
// It returns an error if the log can't be written, so the tray can say so: an
// antivirus blocking writes there would also stop settings from being saved.
func setupLog(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.SetOutput(io.Discard)
		return err
	}
	p := filepath.Join(dir, "kopi.log")
	if st, err := os.Stat(p); err == nil && st.Size() > 1<<20 {
		os.Rename(p, p+".old")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		_, err = fmt.Fprintf(f, "%s kopi %s starting (%s/%s)\n",
			time.Now().Format("2006/01/02 15:04:05"), version, runtime.GOOS, runtime.GOARCH)
	}
	if err != nil {
		log.SetOutput(io.Discard)
		return err
	}
	log.SetOutput(f)
	return nil
}

func (a *kopiApp) onReady() {
	if runtime.GOOS == "darwin" {
		systray.SetTemplateIcon(icon.Template(), icon.Template())
	} else {
		systray.SetIcon(icon.ICO())
	}
	systray.SetTooltip("Kopi")

	a.mStatus = systray.AddMenuItem("Kopi — starting…", "")
	a.mStatus.Disable()
	systray.AddSeparator()
	a.mDevices = systray.AddMenuItem("Devices", "")
	a.mDevices.Disable()
	for i := range a.mPeers {
		a.mPeers[i] = systray.AddMenuItem("", "")
		a.mPeers[i].Disable()
		a.mPeers[i].Hide()
	}
	systray.AddSeparator()
	a.mPause = systray.AddMenuItemCheckbox("Pause sync", "Stop sending and receiving clips", false)
	// The sending device's setting decides whether a sensitive copy leaves it, so the
	// label says "from this device" (rc1 testing: users toggled it on the receiver).
	a.mSensitive = systray.AddMenuItemCheckbox("Send passwords copied on this device",
		"Also sync text that password managers mark as private, when copied here", false)
	a.mPass = systray.AddMenuItem("Set passphrase…", "The same passphrase must be set on every device")
	a.mLogin = systray.AddMenuItemCheckbox("Start at login", "Start Kopi automatically when you log in", a.loginEnabled())
	systray.AddSeparator()
	a.mQuit = systray.AddMenuItem("Quit Kopi", "")

	a.start()
	go a.clicks()
	go a.refresher()
	go a.watchConfig()
}

func (a *kopiApp) onExit() {
	a.cancel()
	a.mu.Lock()
	n := a.node
	a.mu.Unlock()
	if n != nil {
		done := make(chan struct{})
		go func() { n.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}
	log.Print("kopi stopped")
}

// start loads the config (creating it on first run, so the device ID persists) and
// brings the node up.
func (a *kopiApp) start() {
	cfg, err := config.Load(a.cfgPath)
	if err == nil {
		if _, statErr := os.Stat(a.cfgPath); os.IsNotExist(statErr) {
			err = config.Save(a.cfgPath, cfg)
		}
	}
	if err == nil {
		err = cfg.Validate()
	}
	if err != nil {
		a.fail(fmt.Errorf("config: %w", err))
		return
	}
	a.mu.Lock()
	a.cfg = cfg
	a.cfgMtime = mtime(a.cfgPath)
	a.mu.Unlock()
	if cfg.IncludeSensitive {
		a.mSensitive.Check()
	}

	cb, err := clip.New()
	if err != nil {
		a.fail(err)
		return
	}
	n, err := app.Start(a.ctx, app.Options{
		DeviceID:         cfg.DeviceID,
		DeviceName:       cfg.DeviceName,
		Key:              keyFor(cfg.Passphrase),
		Clipboard:        cb,
		ListenAddr:       fmt.Sprintf(":%d", cfg.Port),
		IncludeSensitive: cfg.IncludeSensitive,
		LineEnding:       clip.LineEnding,
		OnEvent:          a.onEvent,
		OnPeersChanged:   a.markDirty,
	})
	if err != nil {
		a.fail(fmt.Errorf("can't listen on port %d (is Kopi already running?): %w", cfg.Port, err))
		return
	}
	a.mu.Lock()
	a.node, a.started = n, time.Now()
	a.mu.Unlock()
	log.Printf("listening on port %d as %q (%.6s)", n.Port(), cfg.DeviceName, cfg.DeviceID)
	a.markDirty()
	time.AfterFunc(noPeersHintAfter+time.Second, a.markDirty) // DISC-7 hint, if still alone
}

func (a *kopiApp) fail(err error) {
	log.Print(err)
	a.mu.Lock()
	a.startErr = err
	a.mu.Unlock()
	a.markDirty()
}

func keyFor(passphrase string) *proto.Key {
	if passphrase == "" {
		return nil
	}
	k := proto.DeriveKey(passphrase)
	return &k
}

// onEvent is called by the engine, sometimes while it holds its lock: it must only
// record and signal, never call back into the engine.
func (a *kopiApp) onEvent(ev ksync.Event) {
	switch ev.Kind {
	case ksync.EventTooLarge:
		a.setFlash("Clip too large (max 15 MB)", 5*time.Second)
	case ksync.EventSkippedSensitive:
		a.setFlash("Skipped a sensitive clip", 3*time.Second)
	case ksync.EventSent, ksync.EventApplied:
		log.Printf("event %d, %d bytes", ev.Kind, ev.Size)
	}
}

func (a *kopiApp) setFlash(msg string, d time.Duration) {
	a.mu.Lock()
	a.flash, a.flashTil = msg, time.Now().Add(d)
	a.mu.Unlock()
	a.markDirty()
	time.AfterFunc(d+50*time.Millisecond, a.markDirty)
}

func (a *kopiApp) markDirty() {
	select {
	case a.dirty <- struct{}{}:
	default:
	}
}

// refresher redraws the menu whenever something changed.
func (a *kopiApp) refresher() {
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-a.dirty:
			a.redraw()
		}
	}
}

func (a *kopiApp) redraw() {
	a.mu.Lock()
	n, startErr, started, writeErr := a.node, a.startErr, a.started, a.writeErr
	flash := ""
	if time.Now().Before(a.flashTil) {
		flash = a.flash
	}
	a.mu.Unlock()

	var peers []transport.PeerStatus
	if n != nil {
		peers = n.Hub.Peers()
	}

	status := "Syncing"
	switch {
	case startErr != nil:
		status = "Can't start — " + startErr.Error()
	case writeErr != nil:
		status = "Can't save settings — is antivirus blocking Kopi? (" + filepath.Dir(a.cfgPath) + ")"
	case n == nil:
		status = "Starting…"
	case !n.Engine.HasKey():
		status = "Set a passphrase to start syncing"
	case n.Engine.Paused():
		status = "Paused"
	case flash != "":
		status = flash
	case len(peers) == 0 && time.Since(started) > noPeersHintAfter:
		status = "No devices found — check firewall, or install Tailscale on both"
	case a.translocated:
		status = "Syncing, but running from a temporary copy — clear quarantine (see README)"
	}
	a.mStatus.SetTitle("Kopi — " + status)
	systray.SetTooltip("Kopi — " + status)
	for i, m := range a.mPeers {
		switch {
		case i < len(peers):
			m.SetTitle("   " + peerLabel(peers[i]))
			m.Show()
		case i == 0:
			m.SetTitle("   No other devices found yet")
			m.Show()
		default:
			m.Hide()
		}
	}
}

func peerLabel(p transport.PeerStatus) string {
	name := p.Name
	if name == "" {
		name = p.ID
	}
	state := map[transport.State]string{
		transport.StateUnknown:     "found",
		transport.StateOK:          "connected",
		transport.StateMismatch:    "passphrase mismatch",
		transport.StateUnreachable: "unreachable",
	}[p.State]
	return name + " — " + state
}

func (a *kopiApp) clicks() {
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-a.mPause.ClickedCh:
			paused := !a.mPause.Checked()
			if paused {
				a.mPause.Check()
			} else {
				a.mPause.Uncheck()
			}
			if n := a.currentNode(); n != nil {
				n.Engine.SetPaused(paused)
			}
			log.Printf("paused=%v", paused)
			a.markDirty()
		case <-a.mSensitive.ClickedCh:
			on := !a.mSensitive.Checked()
			if on {
				a.mSensitive.Check()
			} else {
				a.mSensitive.Uncheck()
			}
			if n := a.currentNode(); n != nil {
				n.Engine.SetIncludeSensitive(on)
			}
			a.updateConfig(func(c *config.Config) { c.IncludeSensitive = on })
			log.Printf("include_sensitive=%v", on)
		case <-a.mPass.ClickedCh:
			// One dialog at a time: further clicks while it's open are ignored.
			if a.dialogOpen.CompareAndSwap(false, true) {
				go func() {
					defer a.dialogOpen.Store(false)
					a.setPassphraseFromTray()
				}()
			}
		case <-a.mLogin.ClickedCh:
			a.toggleLogin()
		case <-a.mQuit.ClickedCh:
			systray.Quit()
			return
		}
	}
}

// loginEnabled reports whether this copy of Kopi starts at login.
func (a *kopiApp) loginEnabled() bool {
	exe, err := autostart.Executable()
	if err != nil {
		return false
	}
	on, err := autostart.Enabled(exe)
	if err != nil {
		log.Printf("start at login: %v", err)
	}
	return on
}

func (a *kopiApp) toggleLogin() {
	exe, err := autostart.Executable()
	if err == nil {
		if a.mLogin.Checked() {
			err = autostart.Disable()
		} else {
			err = autostart.Enable(exe)
		}
	}
	if err != nil {
		log.Printf("start at login: %v", err)
		showMessage("Couldn't change Start at login: " + err.Error())
	}
	// Show what is actually configured, whatever happened.
	if a.loginEnabled() {
		a.mLogin.Check()
	} else {
		a.mLogin.Uncheck()
	}
	log.Printf("start_at_login=%v", a.mLogin.Checked())
}

func (a *kopiApp) setPassphraseFromTray() {
	p, err := askPassphrase(false)
	if err == errCancelled {
		return
	}
	if err != nil {
		showMessage("Passphrase not changed: " + err.Error())
		return
	}
	if err := a.updateConfig(func(c *config.Config) { c.Passphrase = p }); err != nil {
		return // updateConfig already told the user why
	}
	a.applyPassphrase(p)
	showMessage("Passphrase saved. Set the same passphrase on your other devices.")
}

func (a *kopiApp) applyPassphrase(p string) {
	if n := a.currentNode(); n != nil {
		n.Engine.SetKey(keyFor(p))
		n.Hub.ResetStates()
	}
	log.Print("passphrase changed")
	a.markDirty()
}

func (a *kopiApp) currentNode() *app.Node {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.node
}

// updateConfig changes and saves the config, remembering the new mtime so the
// watcher doesn't treat our own write as an outside change.
func (a *kopiApp) updateConfig(change func(*config.Config)) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	change(&a.cfg)
	err := config.Save(a.cfgPath, a.cfg)
	if err != nil {
		log.Printf("saving config: %v", err)
		a.writeErr = err
		go showMessage("Kopi couldn't save its settings: " + err.Error() +
			"\n\nIf you use antivirus with ransomware protection, allow kopi.exe to write to " +
			filepath.Dir(a.cfgPath))
	} else {
		a.writeErr = nil
		a.cfgMtime = mtime(a.cfgPath)
	}
	go a.markDirty()
	return err
}

// watchConfig applies changes made outside the app, e.g. by `kopi passphrase`.
func (a *kopiApp) watchConfig() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-t.C:
		}
		m := mtime(a.cfgPath)
		a.mu.Lock()
		same := m.Equal(a.cfgMtime)
		a.mu.Unlock()
		if same {
			continue
		}
		cfg, err := config.Load(a.cfgPath)
		if err != nil {
			log.Printf("config changed but can't be read: %v", err)
			continue
		}
		a.mu.Lock()
		old := a.cfg
		a.cfg, a.cfgMtime = cfg, m
		a.mu.Unlock()
		if cfg.Passphrase != old.Passphrase {
			a.applyPassphrase(cfg.Passphrase)
		}
		if cfg.IncludeSensitive != old.IncludeSensitive {
			if n := a.currentNode(); n != nil {
				n.Engine.SetIncludeSensitive(cfg.IncludeSensitive)
			}
			if cfg.IncludeSensitive {
				a.mSensitive.Check()
			} else {
				a.mSensitive.Uncheck()
			}
		}
	}
}

func mtime(path string) time.Time {
	st, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}
