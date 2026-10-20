package sync

import (
	"context"
	"errors"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"github.com/umidscodes/kopi/internal/clip"
	"github.com/umidscodes/kopi/internal/proto"
)

var (
	keyAlpha = proto.DeriveKey("alpha")
	keyBravo = proto.DeriveKey("bravo")
)

// memNet connects engines in memory. In immediate mode Broadcast delivers right away;
// with hold set, messages queue until flush, which lets tests reproduce races.
type memNet struct {
	mu      gosync.Mutex
	devices map[string]*device
	hold    bool
	queue   []delivery
}

type delivery struct {
	to     *device
	sealed []byte
}

type device struct {
	id     string
	net    *memNet
	clip   *clip.Fake
	engine *Engine

	mu     gosync.Mutex
	sent   [][]byte
	errs   []error
	events []Event
}

func newNet() *memNet { return &memNet{devices: map[string]*device{}} }

type devOpt func(*Options)

func withKey(k *proto.Key) devOpt     { return func(o *Options) { o.Key = k } }
func lineEnding(s string) devOpt      { return func(o *Options) { o.LineEnding = s } }
func clock(f func() time.Time) devOpt { return func(o *Options) { o.Now = f } }

// add creates a device on the network with the alpha key unless overridden.
func (n *memNet) add(t *testing.T, id string, opts ...devOpt) *device {
	t.Helper()
	d := &device{id: id, net: n, clip: clip.NewFake()}
	o := Options{DeviceID: id, Clipboard: d.clip, Network: d, Key: &keyAlpha}
	o.OnEvent = func(ev Event) {
		d.mu.Lock()
		d.events = append(d.events, ev)
		d.mu.Unlock()
	}
	for _, f := range opts {
		f(&o)
	}
	d.engine = New(o)
	n.mu.Lock()
	n.devices[id] = d
	n.mu.Unlock()
	return d
}

// run starts the device's clipboard watcher until the test ends.
func (d *device) run(t *testing.T) *device {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go d.engine.Run(ctx)
	// Give Run time to register its watcher before tests copy.
	time.Sleep(10 * time.Millisecond)
	return d
}

func (d *device) Broadcast(sealed []byte) {
	d.mu.Lock()
	d.sent = append(d.sent, sealed)
	d.mu.Unlock()
	d.net.mu.Lock()
	var targets []*device
	for _, other := range d.net.devices {
		if other != d {
			targets = append(targets, other)
		}
	}
	hold := d.net.hold
	if hold {
		for _, to := range targets {
			d.net.queue = append(d.net.queue, delivery{to, sealed})
		}
	}
	d.net.mu.Unlock()
	if !hold {
		for _, to := range targets {
			to.deliver(sealed)
		}
	}
}

func (d *device) deliver(sealed []byte) {
	if err := d.engine.Receive(sealed); err != nil {
		d.mu.Lock()
		d.errs = append(d.errs, err)
		d.mu.Unlock()
	}
}

func (n *memNet) flush() {
	n.mu.Lock()
	q := n.queue
	n.queue = nil
	n.mu.Unlock()
	for _, dl := range q {
		dl.to.deliver(dl.sealed)
	}
}

func (d *device) sentCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.sent)
}

func (d *device) errors() []error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]error(nil), d.errs...)
}

func (d *device) hasEvent(k EventKind) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, ev := range d.events {
		if ev.Kind == k {
			return true
		}
	}
	return false
}

// eventually polls cond until it is true or the timeout passes.
func eventually(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out after %v waiting for: %s", timeout, what)
}

// settle waits long enough for any stray echoes or re-broadcasts to happen.
func settle() { time.Sleep(100 * time.Millisecond) }

func hasText(d *device, s string) func() bool { return func() bool { return d.clip.Text() == s } }

// --- Feature: Sync ---

func TestSYNC1_CopiedTextAppearsOnPeer(t *testing.T) {
	n := newNet()
	a := n.add(t, "a").run(t)
	b := n.add(t, "b").run(t)
	a.clip.Copy("hello", false)
	eventually(t, time.Second, `B has "hello"`, hasText(b, "hello"))
}

func TestSYNC2_BothDirections(t *testing.T) {
	n := newNet()
	a := n.add(t, "a").run(t)
	b := n.add(t, "b").run(t)
	a.clip.Copy("from A", false)
	eventually(t, time.Second, `B has "from A"`, hasText(b, "from A"))
	b.clip.Copy("from B", false)
	eventually(t, time.Second, `A has "from B"`, hasText(a, "from B"))
}

func TestSYNC3_ReachesEveryPeer(t *testing.T) {
	n := newNet()
	a := n.add(t, "a").run(t)
	b := n.add(t, "b").run(t)
	c := n.add(t, "c").run(t)
	a.clip.Copy("all of you", false)
	eventually(t, time.Second, "B has it", hasText(b, "all of you"))
	eventually(t, time.Second, "C has it", hasText(c, "all of you"))
}

func TestSYNC4_ReceivedClipIsNotRebroadcast(t *testing.T) {
	n := newNet()
	a := n.add(t, "a").run(t)
	b := n.add(t, "b").run(t)
	c := n.add(t, "c").run(t)
	a.clip.Copy("once", false)
	eventually(t, time.Second, "B and C have it", func() bool {
		return b.clip.Text() == "once" && c.clip.Text() == "once"
	})
	settle()
	if got := a.sentCount(); got != 1 {
		t.Errorf("A sent %d messages, want 1", got)
	}
	if got := b.sentCount() + c.sentCount(); got != 0 {
		t.Errorf("B and C re-broadcast %d messages, want 0", got)
	}
	if b.clip.Writes() != 1 || c.clip.Writes() != 1 {
		t.Errorf("writes: B=%d C=%d, want exactly 1 each", b.clip.Writes(), c.clip.Writes())
	}
}

func TestSYNC5_CopyingSameTextAgainStillSyncs(t *testing.T) {
	n := newNet()
	a := n.add(t, "a").run(t)
	b := n.add(t, "b").run(t)
	// "x" arrives on A from B, so A's last applied clip is "x".
	b.clip.Copy("x", false)
	eventually(t, time.Second, `A has "x"`, hasText(a, "x"))
	settle()
	a.clip.Copy("y", false)
	eventually(t, time.Second, `B has "y"`, hasText(b, "y"))
	a.clip.Copy("x", false)
	eventually(t, time.Second, `B has "x" again`, hasText(b, "x"))
}

func TestSYNC5_RapidCopiesOnOneDeviceAllSync(t *testing.T) {
	// Copies within the same millisecond must not be dropped as "older".
	fixed := time.UnixMilli(1_790_000_000_000)
	now := func() time.Time { return fixed }
	n := newNet()
	a := n.add(t, "a", clock(now))
	b := n.add(t, "b", clock(now))
	for _, s := range []string{"1", "2", "3"} {
		a.clip.Copy(s, false)
		a.engine.HandleLocal(clip.Item{Text: s})
	}
	if got := b.clip.Text(); got != "3" {
		t.Fatalf("B has %q, want %q", got, "3")
	}
	if errs := b.errors(); len(errs) != 0 {
		t.Fatalf("B rejected messages: %v", errs)
	}
}

func TestSYNC6_LatestCopyWins(t *testing.T) {
	t0 := time.UnixMilli(1_790_000_000_000)
	var mu gosync.Mutex
	cur := t0
	now := func() time.Time { mu.Lock(); defer mu.Unlock(); return cur }
	n := newNet()
	n.hold = true // both copies are in flight at the same time
	a := n.add(t, "a", clock(now))
	b := n.add(t, "b", clock(now))

	a.clip.Copy("first", false)
	a.engine.HandleLocal(clip.Item{Text: "first"})
	mu.Lock()
	cur = t0.Add(50 * time.Millisecond)
	mu.Unlock()
	b.clip.Copy("second", false)
	b.engine.HandleLocal(clip.Item{Text: "second"})

	n.flush()
	if a.clip.Text() != "second" || b.clip.Text() != "second" {
		t.Fatalf("A=%q B=%q, want both %q", a.clip.Text(), b.clip.Text(), "second")
	}
}

func TestSYNC7_UnicodeAndLineEndings(t *testing.T) {
	n := newNet()
	a := n.add(t, "a").run(t)
	win := n.add(t, "win", lineEnding("\r\n")).run(t)
	mac := n.add(t, "mac", lineEnding("\n")).run(t)
	a.clip.Copy("héllo 🌍 你好\r\nline2", false)
	eventually(t, time.Second, "Windows peer gets CRLF", hasText(win, "héllo 🌍 你好\r\nline2"))
	eventually(t, time.Second, "Mac peer gets LF", hasText(mac, "héllo 🌍 你好\nline2"))
	settle()
	// The CRLF echo on the Windows peer must still be recognised as its own write.
	if win.sentCount() != 0 || mac.sentCount() != 0 {
		t.Fatalf("peers re-broadcast: win=%d mac=%d", win.sentCount(), mac.sentCount())
	}
}

// --- Feature: Security ---

func TestSEC1_WrongPassphraseCannotSync(t *testing.T) {
	n := newNet()
	a := n.add(t, "a", withKey(&keyAlpha)).run(t)
	b := n.add(t, "b", withKey(&keyBravo)).run(t)
	a.clip.Copy("secret", false)
	eventually(t, time.Second, "B reports an error", func() bool { return len(b.errors()) > 0 })
	if !errors.Is(b.errors()[0], proto.ErrDecrypt) {
		t.Fatalf("B error = %v, want proto.ErrDecrypt", b.errors()[0])
	}
	if b.clip.Text() != "" {
		t.Fatalf("B clipboard changed to %q", b.clip.Text())
	}
}

func TestSEC3_ReplayedMessageIgnored(t *testing.T) {
	n := newNet()
	a := n.add(t, "a").run(t)
	b := n.add(t, "b").run(t)
	a.clip.Copy("pay $10", false)
	eventually(t, time.Second, "B has it", hasText(b, "pay $10"))
	captured := a.sent[0]

	b.clip.Copy("something else", false)
	eventually(t, time.Second, "A has B's clip", hasText(a, "something else"))
	b.deliver(captured) // attacker re-sends the old message
	if b.clip.Text() != "something else" {
		t.Fatalf("replay changed B's clipboard to %q", b.clip.Text())
	}
	errs := b.errors()
	if len(errs) == 0 || !errors.Is(errs[len(errs)-1], proto.ErrReplay) {
		t.Fatalf("B errors = %v, want ErrReplay", errs)
	}
}

func TestSEC4_NoPassphraseNoSync(t *testing.T) {
	n := newNet()
	a := n.add(t, "a", withKey(nil)).run(t)
	b := n.add(t, "b").run(t)
	a.clip.Copy("hello", false)
	eventually(t, time.Second, "A reports no passphrase", func() bool { return a.hasEvent(EventNoPassphrase) })
	if a.sentCount() != 0 {
		t.Fatal("A sent without a passphrase")
	}
	b.clip.Copy("to A", false)
	eventually(t, time.Second, "A rejects", func() bool { return len(a.errors()) > 0 })
	if !errors.Is(a.errors()[0], ErrNoPassphrase) || a.clip.Text() != "hello" {
		t.Fatalf("A errors=%v clipboard=%q", a.errors(), a.clip.Text())
	}
}

func TestSEC4_SettingPassphraseLaterStartsSync(t *testing.T) {
	n := newNet()
	a := n.add(t, "a", withKey(nil)).run(t)
	b := n.add(t, "b").run(t)
	a.clip.Copy("before", false)
	eventually(t, time.Second, "A refuses without passphrase", func() bool { return a.hasEvent(EventNoPassphrase) })
	if a.engine.HasKey() {
		t.Fatal("HasKey true without a passphrase")
	}
	a.engine.SetKey(&keyAlpha)
	a.clip.Copy("after", false)
	eventually(t, time.Second, "B receives once A has the passphrase", hasText(b, "after"))
}

func TestSEC1_ChangingPassphraseCutsOffOldPeers(t *testing.T) {
	n := newNet()
	a := n.add(t, "a").run(t)
	b := n.add(t, "b").run(t)
	a.clip.Copy("same key", false)
	eventually(t, time.Second, "B has it", hasText(b, "same key"))
	a.engine.SetKey(&keyBravo)
	a.clip.Copy("new key", false)
	eventually(t, time.Second, "B rejects", func() bool { return len(b.errors()) > 0 })
	if b.clip.Text() != "same key" || !errors.Is(b.errors()[0], proto.ErrDecrypt) {
		t.Fatalf("B clipboard=%q errors=%v", b.clip.Text(), b.errors())
	}
}

// --- Feature: Sensitive content ---

func TestSENS1_SensitiveSkippedByDefault(t *testing.T) {
	n := newNet()
	a := n.add(t, "a").run(t)
	b := n.add(t, "b").run(t)
	a.clip.Copy("hunter2", true)
	eventually(t, time.Second, "A skips it", func() bool { return a.hasEvent(EventSkippedSensitive) })
	settle()
	if a.sentCount() != 0 || b.clip.Text() != "" {
		t.Fatalf("sensitive clip was sent: sent=%d B=%q", a.sentCount(), b.clip.Text())
	}
}

func TestSENS2_SensitiveCanBeOptedIn(t *testing.T) {
	n := newNet()
	a := n.add(t, "a").run(t)
	b := n.add(t, "b").run(t)
	a.engine.SetIncludeSensitive(true)
	a.clip.Copy("hunter2", true)
	eventually(t, time.Second, "B has it", hasText(b, "hunter2"))
}

// --- Feature: Size limit ---

func TestSIZE1_OversizedClipNotSent(t *testing.T) {
	n := newNet()
	a := n.add(t, "a").run(t)
	n.add(t, "b").run(t)
	a.clip.Copy(strings.Repeat("x", proto.MaxTextBytes+1), false)
	eventually(t, 2*time.Second, "A reports too large", func() bool { return a.hasEvent(EventTooLarge) })
	if a.sentCount() != 0 {
		t.Fatal("oversized clip was sent")
	}
}

func TestSIZE3_ExactlyAtLimitSyncs(t *testing.T) {
	n := newNet()
	a := n.add(t, "a").run(t)
	b := n.add(t, "b").run(t)
	big := strings.Repeat("x", proto.MaxTextBytes)
	a.clip.Copy(big, false)
	eventually(t, 5*time.Second, "B has the 15 MB clip", func() bool { return len(b.clip.Text()) == proto.MaxTextBytes })
}

// --- Feature: Tray ---

func TestTRAY2_PauseStopsBothDirections(t *testing.T) {
	n := newNet()
	a := n.add(t, "a").run(t)
	b := n.add(t, "b").run(t)
	a.engine.SetPaused(true)
	a.clip.Copy("from A", false)
	b.clip.Copy("from B", false)
	settle()
	if a.sentCount() != 0 {
		t.Error("paused device sent a clip")
	}
	if a.clip.Text() != "from A" {
		t.Errorf("paused device's clipboard changed to %q", a.clip.Text())
	}
	a.engine.SetPaused(false)
	a.clip.Copy("resumed", false)
	eventually(t, time.Second, "B gets clips after resume", hasText(b, "resumed"))
}

// --- Feature: Recent clips (engine recording) ---

// recEntry is one call to the fake Recorder.
type recEntry struct {
	text string
	from string
	at   time.Time
}

// fakeRec records what the engine hands to Options.Recent.
type fakeRec struct {
	mu  gosync.Mutex
	got []recEntry
}

func (r *fakeRec) Record(text, from string, at time.Time) {
	r.mu.Lock()
	r.got = append(r.got, recEntry{text, from, at})
	r.mu.Unlock()
}

func (r *fakeRec) entries() []recEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recEntry(nil), r.got...)
}

func (r *fakeRec) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.got)
}

func withRecent(rec Recorder) devOpt { return func(o *Options) { o.Recent = rec } }

func TestRECENT1_SentClipRecorded(t *testing.T) {
	fixed := time.UnixMilli(1_790_000_000_000)
	now := func() time.Time { return fixed }
	n := newNet()
	recA := &fakeRec{}
	a := n.add(t, "a", clock(now), withRecent(recA))
	b := n.add(t, "b", clock(now))

	a.engine.HandleLocal(clip.Item{Text: "hello"})
	a.engine.HandleLocal(clip.Item{Text: "hi\r\nthere"})

	got := recA.entries()
	if len(got) != 2 {
		t.Fatalf("A recorded %d entries, want 2: %+v", len(got), got)
	}
	if got[0].text != "hello" || got[0].from != "a" || !got[0].at.Equal(fixed) {
		t.Errorf("A recorded %+v, want {hello a %v}", got[0], fixed)
	}
	// The recorded text is normalized (\r\n -> \n), and the source is our own device.
	if got[1].text != "hi\nthere" || got[1].from != "a" {
		t.Errorf("A recorded %+v, want {\"hi\\nthere\" a}", got[1])
	}
	if b.clip.Text() != "hi\nthere" {
		t.Errorf("B clipboard = %q", b.clip.Text())
	}
}

func TestRECENT2_AppliedClipRecordedWithSender(t *testing.T) {
	tA := time.UnixMilli(1_790_000_000_000)
	tB := tA.Add(30 * time.Second) // B's clock is not A's: the recorded time is local (RECENT-10)
	n := newNet()
	recB := &fakeRec{}
	a := n.add(t, "a", clock(func() time.Time { return tA }))
	b := n.add(t, "b", clock(func() time.Time { return tB }), withRecent(recB))

	a.engine.HandleLocal(clip.Item{Text: "hello"})

	if b.clip.Text() != "hello" {
		t.Fatalf("B clipboard = %q, want \"hello\"", b.clip.Text())
	}
	got := recB.entries()
	if len(got) != 1 {
		t.Fatalf("B recorded %d entries, want 1: %+v", len(got), got)
	}
	if got[0].text != "hello" || got[0].from != "a" || !got[0].at.Equal(tB) {
		t.Errorf("B recorded %+v, want {hello a %v} (local Now, not m.SentAt)", got[0], tB)
	}
}

// A device's own sends must be recorded at its own clock (RECENT-10), not at the
// SYNC-6 wire timestamp, which is bumped past a peer's ahead-in-time clip.
func TestRECENT10_SentClipRecordedWithOwnClock(t *testing.T) {
	tA := time.UnixMilli(1_790_000_000_000)
	tB := tA.Add(90 * time.Second) // B's clock runs 90 s ahead of A's
	n := newNet()
	recA := &fakeRec{}
	a := n.add(t, "a", clock(func() time.Time { return tA }), withRecent(recA))
	b := n.add(t, "b", clock(func() time.Time { return tB }))

	b.engine.HandleLocal(clip.Item{Text: "from b"}) // A applies it, so A's lastAt = tB
	if a.clip.Text() != "from b" {
		t.Fatalf("A clipboard = %q, want \"from b\"", a.clip.Text())
	}
	a.engine.HandleLocal(clip.Item{Text: "from a"})

	if a.sentCount() != 1 {
		t.Fatalf("A sent %d messages, want 1", a.sentCount())
	}
	m, err := proto.Open(keyAlpha, a.sent[0])
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// The wire keeps the bumped, strictly-increasing SYNC-6 timestamp...
	if !m.SentAt.Equal(tB.Add(time.Millisecond)) {
		t.Errorf("wire SentAt = %v, want %v (SYNC-6 bump kept)", m.SentAt, tB.Add(time.Millisecond))
	}
	// ...but the Recent entries use this device's own clock (RECENT-10). The applied
	// clip is recorded first (inside B's broadcast), then A's own send.
	got := recA.entries()
	if len(got) != 2 {
		t.Fatalf("A recorded %d entries, want 2: %+v", len(got), got)
	}
	if got[0].text != "from b" || got[0].from != "b" || !got[0].at.Equal(tA) {
		t.Errorf("A recorded %+v for the applied clip, want {from b b %v}", got[0], tA)
	}
	if got[1].text != "from a" || got[1].from != "a" || !got[1].at.Equal(tA) {
		t.Errorf("A recorded %+v for its own send, want {from a a %v} (own clock, not peer's)", got[1], tA)
	}
}

// The recorder gets the normalized text, not the clipboard's native line endings:
// RECENT-4 dedupe compares raw text, so "a\nb" copied locally and "a\nb" received
// on a CRLF device must be one entry.
func TestRECENT4_ReceivedTextRecordedNormalized(t *testing.T) {
	n := newNet()
	recB := &fakeRec{}
	a := n.add(t, "a")
	b := n.add(t, "b", lineEnding("\r\n"), withRecent(recB))

	a.engine.HandleLocal(clip.Item{Text: "x\r\ny"})

	if b.clip.Text() != "x\r\ny" {
		t.Fatalf("B clipboard = %q, want \"x\\r\\ny\"", b.clip.Text())
	}
	got := recB.entries()
	if len(got) != 1 {
		t.Fatalf("B recorded %d entries, want 1: %+v", len(got), got)
	}
	if got[0].text != "x\ny" {
		t.Errorf("B recorded %q, want the normalized %q (RECENT-4 dedupe)", got[0].text, "x\ny")
	}
	if got[0].from != "a" {
		t.Errorf("B recorded from %q, want \"a\"", got[0].from)
	}
}

func TestRECENT16_UnsyncedClipsNotRecorded(t *testing.T) {
	t.Run("paused", func(t *testing.T) {
		n := newNet()
		recA, recB := &fakeRec{}, &fakeRec{}
		a := n.add(t, "a", withRecent(recA))
		b := n.add(t, "b", withRecent(recB))
		a.engine.SetPaused(true)
		a.engine.HandleLocal(clip.Item{Text: "not-synced"})
		if recA.count() != 0 || recB.count() != 0 || b.clip.Text() != "" {
			t.Errorf("paused send recorded: A=%d B=%d Bclip=%q, want 0 and empty",
				recA.count(), recB.count(), b.clip.Text())
		}
	})
	t.Run("no passphrase", func(t *testing.T) {
		n := newNet()
		recA, recB := &fakeRec{}, &fakeRec{}
		a := n.add(t, "a", withKey(nil), withRecent(recA))
		b := n.add(t, "b", withRecent(recB))
		a.engine.HandleLocal(clip.Item{Text: "not-synced"})
		if recA.count() != 0 || recB.count() != 0 || b.clip.Text() != "" {
			t.Errorf("no-passphrase send recorded: A=%d B=%d Bclip=%q, want 0 and empty",
				recA.count(), recB.count(), b.clip.Text())
		}
	})
	t.Run("too large", func(t *testing.T) {
		n := newNet()
		recA, recB := &fakeRec{}, &fakeRec{}
		a := n.add(t, "a", withRecent(recA))
		b := n.add(t, "b", withRecent(recB))
		a.engine.HandleLocal(clip.Item{Text: strings.Repeat("x", proto.MaxTextBytes+1)})
		if recA.count() != 0 || recB.count() != 0 || b.clip.Text() != "" {
			t.Errorf("oversized send recorded: A=%d B=%d Bclip=%q, want 0 and empty",
				recA.count(), recB.count(), b.clip.Text())
		}
	})
	t.Run("sensitive skipped", func(t *testing.T) {
		n := newNet()
		recA, recB := &fakeRec{}, &fakeRec{}
		a := n.add(t, "a", withRecent(recA))
		b := n.add(t, "b", withRecent(recB))
		a.engine.HandleLocal(clip.Item{Text: "not-synced", Sensitive: true})
		if recA.count() != 0 || recB.count() != 0 || b.clip.Text() != "" {
			t.Errorf("skipped sensitive recorded: A=%d B=%d Bclip=%q, want 0 and empty",
				recA.count(), recB.count(), b.clip.Text())
		}
	})
	t.Run("echo of an applied clip", func(t *testing.T) {
		n := newNet()
		recA, recB := &fakeRec{}, &fakeRec{}
		a := n.add(t, "a", withRecent(recA))
		b := n.add(t, "b", withRecent(recB))
		b.engine.HandleLocal(clip.Item{Text: "x"}) // A applies it, recording once
		a.engine.HandleLocal(clip.Item{Text: "x"}) // the clipboard echo of A's own write
		if a.sentCount() != 0 {
			t.Errorf("A re-broadcast the echo: sent=%d", a.sentCount())
		}
		if recA.count() != 1 || recB.count() != 1 {
			t.Errorf("echo recorded: A=%d B=%d, want 1 each", recA.count(), recB.count())
		}
		if got := recA.entries()[0]; got.text != "x" || got.from != "b" {
			t.Errorf("A recorded %+v from the apply, want {x b}", got)
		}
	})
}

func TestRECENT17_UnappliedClipsNotRecorded(t *testing.T) {
	t.Run("lost to a newer clip SYNC-6", func(t *testing.T) {
		t0 := time.UnixMilli(1_790_000_000_000)
		var mu gosync.Mutex
		cur := t0
		now := func() time.Time { mu.Lock(); defer mu.Unlock(); return cur }
		n := newNet()
		n.hold = true
		recB := &fakeRec{}
		a := n.add(t, "a", clock(now))
		b := n.add(t, "b", clock(now), withRecent(recB))
		a.clip.Copy("first", false)
		a.engine.HandleLocal(clip.Item{Text: "first"})
		mu.Lock()
		cur = t0.Add(50 * time.Millisecond)
		mu.Unlock()
		b.clip.Copy("second", false)
		b.engine.HandleLocal(clip.Item{Text: "second"})
		n.flush()
		if b.clip.Text() != "second" {
			t.Fatalf("B clipboard = %q, want \"second\"", b.clip.Text())
		}
		got := recB.entries()
		if len(got) != 1 || got[0].text != "second" || got[0].from != "b" {
			t.Errorf("B recorded %+v, want just its own \"second\"", got)
		}
	})
	t.Run("replayed message SEC-3", func(t *testing.T) {
		n := newNet()
		recB := &fakeRec{}
		a := n.add(t, "a")
		b := n.add(t, "b", withRecent(recB))
		a.engine.HandleLocal(clip.Item{Text: "pay $10"})
		captured := a.sent[0]
		b.engine.HandleLocal(clip.Item{Text: "something else"})
		b.deliver(captured) // attacker re-sends the old message
		got := recB.entries()
		if len(got) != 2 {
			t.Fatalf("B recorded %d entries after a replay, want 2: %+v", len(got), got)
		}
		if got[0].text != "pay $10" || got[0].from != "a" {
			t.Errorf("B recorded %+v, want the original apply of \"pay $10\"", got[0])
		}
		if got[1].text != "something else" || got[1].from != "b" {
			t.Errorf("B recorded %+v, want its own \"something else\"", got[1])
		}
	})
	t.Run("receiver paused", func(t *testing.T) {
		n := newNet()
		recA, recB := &fakeRec{}, &fakeRec{}
		a := n.add(t, "a", withRecent(recA))
		b := n.add(t, "b", withRecent(recB))
		b.engine.SetPaused(true)
		a.engine.HandleLocal(clip.Item{Text: "from A"})
		if recB.count() != 0 {
			t.Errorf("paused B recorded %d entries, want 0", recB.count())
		}
		if recA.count() != 1 {
			t.Errorf("A recorded %d entries, want its own sent clip only", recA.count())
		}
	})
	t.Run("own message", func(t *testing.T) {
		n := newNet()
		recA := &fakeRec{}
		a := n.add(t, "a", withRecent(recA))
		a.engine.HandleLocal(clip.Item{Text: "solo"})
		sealed := a.sent[0]
		if err := a.engine.Receive(sealed); err != nil {
			t.Fatalf("A rejected its own message: %v", err)
		}
		if recA.count() != 1 {
			t.Errorf("A recorded %d entries, want 1 (the send, not the loopback)", recA.count())
		}
	})
	t.Run("undecryptable", func(t *testing.T) {
		n := newNet()
		recB := &fakeRec{}
		a := n.add(t, "a", withKey(&keyBravo))
		b := n.add(t, "b", withRecent(recB))
		a.engine.HandleLocal(clip.Item{Text: "wrong-key"})
		if recB.count() != 0 {
			t.Errorf("B recorded %d entries for a clip it could not decrypt, want 0", recB.count())
		}
		if !errors.Is(b.errors()[0], proto.ErrDecrypt) {
			t.Errorf("B errors = %v, want ErrDecrypt", b.errors())
		}
	})
}

func TestRECENT18_SensitiveNotRecordedOnSender(t *testing.T) {
	n := newNet()
	recA := &fakeRec{}
	a := n.add(t, "a", withRecent(recA))
	b := n.add(t, "b")
	a.engine.SetIncludeSensitive(true)

	a.engine.HandleLocal(clip.Item{Text: "hunter2", Sensitive: true})

	if a.sentCount() != 1 {
		t.Fatalf("A sent %d messages, want the sensitive clip to sync (SENS-2)", a.sentCount())
	}
	if recA.count() != 0 {
		t.Errorf("A recorded %d entries for a sensitive clip, want 0", recA.count())
	}
	if b.clip.Text() != "hunter2" {
		t.Errorf("B clipboard = %q, want \"hunter2\" (SENS-2: B must receive it)", b.clip.Text())
	}
}

func TestRECENT19_SensitiveNotRecordedOnReceiver(t *testing.T) {
	n := newNet()
	recB := &fakeRec{}
	a := n.add(t, "a")
	b := n.add(t, "b", withRecent(recB))
	a.engine.SetIncludeSensitive(true)

	a.engine.HandleLocal(clip.Item{Text: "hunter2", Sensitive: true})

	if b.clip.Text() != "hunter2" {
		t.Fatalf("B clipboard = %q, want \"hunter2\" (SENS-2)", b.clip.Text())
	}
	if recB.count() != 0 {
		t.Errorf("B recorded %d entries for a sensitive clip, want 0", recB.count())
	}
}

func TestRECENT22_SensitiveRulesUnchanged(t *testing.T) {
	t.Run("SENS-1 skipped by default", func(t *testing.T) {
		n := newNet()
		recA, recB := &fakeRec{}, &fakeRec{}
		a := n.add(t, "a", withRecent(recA))
		b := n.add(t, "b", withRecent(recB))
		a.engine.HandleLocal(clip.Item{Text: "hunter2", Sensitive: true})
		if !a.hasEvent(EventSkippedSensitive) || a.sentCount() != 0 || b.clip.Text() != "" {
			t.Errorf("SENS-1 changed: sent=%d B=%q", a.sentCount(), b.clip.Text())
		}
		if recA.count() != 0 || recB.count() != 0 {
			t.Error("skipped sensitive clip was recorded")
		}
	})
	t.Run("SENS-2 opt-in carries the flag on the wire", func(t *testing.T) {
		n := newNet()
		recB := &fakeRec{}
		a := n.add(t, "a")
		b := n.add(t, "b", withRecent(recB))
		a.engine.SetIncludeSensitive(true)
		a.engine.HandleLocal(clip.Item{Text: "hunter2", Sensitive: true})

		if a.sentCount() != 1 {
			t.Fatalf("A sent %d messages, want 1", a.sentCount())
		}
		m, err := proto.Open(keyAlpha, a.sent[0])
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if !m.Sensitive {
			t.Error("sent message does not carry the sensitive flag (RECENT-22)")
		}
		if m.Text != "hunter2" {
			t.Errorf("sent text = %q", m.Text)
		}
		// The receiver still applies it; only the recent-clips listing changes.
		if b.clip.Text() != "hunter2" {
			t.Errorf("B clipboard = %q, want \"hunter2\"", b.clip.Text())
		}
		if recB.count() != 0 {
			t.Errorf("B recorded %d entries for a sensitive clip, want 0", recB.count())
		}
	})
}
