package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	gosync "sync"
	"time"

	"github.com/umidscodes/kopi/internal/transport"
)

const (
	// TailnetInterval is how often the tailnet's device list is checked.
	TailnetInterval = 10 * time.Second
	// TailnetTimeout drops a tailnet peer after this long without answering (DISC-2).
	TailnetTimeout = 3 * TailnetInterval
	helloTimeout   = 3 * time.Second
)

// Tailscale finds Kopi peers on the user's tailnet: it lists online devices with the
// Tailscale CLI and keeps those that answer a Kopi hello on Port (DISC-4, DISC-6).
// Without a Tailscale CLI it does nothing.
type Tailscale struct {
	Self transport.Peer
	Port int

	// Overridable for tests.
	Status func(ctx context.Context) ([]TailnetDevice, error)
	Hello  func(ctx context.Context, addr string) (transport.Identity, error)
}

// TailnetDevice is one online device from `tailscale status --json`.
type TailnetDevice struct {
	Name string
	IP   netip.Addr
}

func (t Tailscale) Run(ctx context.Context, emit func(Event)) error {
	status, hello := t.Status, t.Hello
	if status == nil {
		status = tailscaleStatus
	}
	if hello == nil {
		hello = transport.Hello
	}
	b := newBrowser(t.Self.ID, emit)
	b.timeout = TailnetTimeout
	tick := time.NewTicker(TailnetInterval)
	defer tick.Stop()
	for {
		if devices, err := status(ctx); err == nil {
			var wg gosync.WaitGroup
			for _, d := range devices {
				wg.Add(1)
				go func(d TailnetDevice) {
					defer wg.Done()
					addr := netip.AddrPortFrom(d.IP, uint16(t.Port))
					hctx, cancel := context.WithTimeout(ctx, helloTimeout)
					id, err := hello(hctx, net.JoinHostPort(d.IP.String(), strconv.Itoa(t.Port)))
					cancel()
					if err == nil && id.ID != "" {
						v := id.Version
						if v < 0 {
							v = 0 // RECENT-21 decision 3: unreadable version = unknown, normal states
						}
						b.seen(transport.Peer{ID: id.ID, Name: id.Name, Version: v, Addrs: []netip.AddrPort{addr}})
					}
				}(d)
			}
			wg.Wait()
		}
		b.expire()
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

var errNoTailscale = errors.New("tailscale CLI not found")

// tailscaleStatus lists online peers (not this device) with their tailnet IPv4.
func tailscaleStatus(ctx context.Context) ([]TailnetDevice, error) {
	cli := tailscaleCLI()
	if cli == "" {
		return nil, errNoTailscale
	}
	cmd := exec.CommandContext(ctx, cli, "status", "--json")
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseTailscaleStatus(out)
}

func parseTailscaleStatus(out []byte) ([]TailnetDevice, error) {
	var st struct {
		Peer map[string]struct {
			HostName     string
			TailscaleIPs []string
			Online       bool
		}
	}
	if err := json.Unmarshal(out, &st); err != nil {
		return nil, err
	}
	var devices []TailnetDevice
	for _, p := range st.Peer {
		if !p.Online {
			continue
		}
		for _, s := range p.TailscaleIPs {
			if ip, err := netip.ParseAddr(s); err == nil && ip.Is4() {
				devices = append(devices, TailnetDevice{Name: p.HostName, IP: ip})
				break
			}
		}
	}
	return devices, nil
}

func tailscaleCLI() string {
	if p, err := exec.LookPath("tailscale"); err == nil {
		return p
	}
	var candidates []string
	switch runtime.GOOS {
	case "windows":
		candidates = []string{`C:\Program Files\Tailscale\tailscale.exe`}
	case "darwin":
		candidates = []string{
			"/Applications/Tailscale.app/Contents/MacOS/Tailscale",
			"/usr/local/bin/tailscale",
			"/opt/homebrew/bin/tailscale",
		}
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

// Multi runs several discovery sources and merges them: a peer is Up while any
// source sees it and Down only when the last one loses it (DISC-5).
type Multi []Discovery

func (m Multi) Run(ctx context.Context, emit func(Event)) error {
	var mu gosync.Mutex
	owners := map[string]map[int]bool{}
	var wg gosync.WaitGroup
	for i, d := range m {
		wg.Add(1)
		go func(i int, d Discovery) {
			defer wg.Done()
			d.Run(ctx, func(ev Event) {
				id := ev.Peer.ID
				mu.Lock()
				if ev.Up {
					if owners[id] == nil {
						owners[id] = map[int]bool{}
					}
					owners[id][i] = true
					mu.Unlock()
					emit(ev)
					return
				}
				was := owners[id][i]
				delete(owners[id], i)
				last := was && len(owners[id]) == 0
				if last {
					delete(owners, id)
				}
				mu.Unlock()
				if last {
					emit(ev)
				}
			})
		}(i, d)
	}
	wg.Wait()
	return nil
}
