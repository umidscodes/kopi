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

// A peer's clip is applied on B, and B's user copies something new before B has
// handled the echo of that Write. The engine can't tell a late echo of "one" from the
// user copying "one" again (which must sync, SYNC-5), so it relies on the clipboard
// reporting changes in the order they happened, as the polled OS clipboards do. The
// fake guarantees that order (one queue per watcher); this test drives B's
// HandleLocal by hand from the watcher, so no goroutine timing is involved.
func TestSYNC4_LateEchoIsNotRebroadcast(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := newNet()
	a := n.add(t, "a")
	b := n.add(t, "b")
	changes, err := b.clip.Watch(ctx)
	if err != nil {
		t.Fatal(err)
	}

	a.clip.Copy("one", false)
	a.engine.HandleLocal(clip.Item{Text: "one"}) // B applies "one": its Write echoes
	b.clip.Copy("two", false)                    // B's user copies before the echo is handled

	for i := 0; i < 2; i++ {
		select {
		case it := <-changes:
			b.engine.HandleLocal(it)
		case <-time.After(5 * time.Second):
			t.Fatalf("change %d never reached B's watcher", i+1)
		}
	}
	if got := b.sentCount(); got != 1 {
		t.Fatalf("B sent %d messages, want 1 (only \"two\")", got)
	}
	if m := lastSent(t, b); m.Text != "two" {
		t.Fatalf("B sent %q, want %q", m.Text, "two")
	}
	if got := a.clip.Text(); got != "two" {
		t.Fatalf("A's clipboard = %q, want %q", got, "two")
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

// --- Feature: Recent clips, restore ---

// stepClock is a test clock that only moves when the test says so.
type stepClock struct {
	mu  gosync.Mutex
	cur time.Time
}

func (c *stepClock) now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.cur }
func (c *stepClock) set(t time.Time) { c.mu.Lock(); c.cur = t; c.mu.Unlock() }

// lastSent opens the most recent message d broadcast.
func lastSent(t *testing.T, d *device) proto.Message {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.sent) == 0 {
		t.Fatalf("%s sent nothing", d.id)
	}
	m, err := proto.Open(keyAlpha, d.sent[len(d.sent)-1])
	if err != nil {
		t.Fatalf("open %s's last message: %v", d.id, err)
	}
	return m
}

func lastRec(t *testing.T, r *fakeRec) recEntry {
	t.Helper()
	got := r.entries()
	if len(got) == 0 {
		t.Fatal("nothing recorded")
	}
	return got[len(got)-1]
}

// These tests drive HandleLocal directly (no Run) so the clipboard echo of the
// engine's own Write is delivered in order, without depending on goroutine timing.

func TestRECENT5_RestoreWritesAndSyncsOnce(t *testing.T) {
	t0 := time.UnixMilli(1_790_000_000_000)
	ck := &stepClock{cur: t0}
	n := newNet()
	recA, recB := &fakeRec{}, &fakeRec{}
	a := n.add(t, "a", clock(ck.now), withRecent(recA))
	b := n.add(t, "b", clock(ck.now), withRecent(recB))
	// A's Recent clips list "y", "x".
	a.engine.HandleLocal(clip.Item{Text: "x"})
	first := lastSent(t, a)
	a.engine.HandleLocal(clip.Item{Text: "y"})
	prev := lastSent(t, a)

	later := t0.Add(5 * time.Second)
	ck.set(later)
	if err := a.engine.Restore("x"); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := a.clip.Text(); got != "x" {
		t.Errorf("A clipboard = %q, want %q", got, "x")
	}
	if got := a.sentCount(); got != 3 {
		t.Fatalf("A sent %d messages, want 3 (x, y, restore)", got)
	}
	m := lastSent(t, a)
	if m.Text != "x" || m.From != "a" || m.Sensitive {
		t.Errorf("restore message = {Text:%q From:%q Sensitive:%v}, want {x a false}", m.Text, m.From, m.Sensitive)
	}
	if m.ID == first.ID || m.ID == prev.ID {
		t.Errorf("restore reused an old message ID")
	}
	if !m.SentAt.After(prev.SentAt) {
		t.Errorf("restore SentAt %v not after previous %v", m.SentAt, prev.SentAt)
	}
	// The clipboard change caused by the restore's own Write is not sent again.
	a.engine.HandleLocal(clip.Item{Text: "x"})
	if got := a.sentCount(); got != 3 {
		t.Errorf("A re-sent the echo of its restore: sent=%d, want 3", got)
	}
	if got := lastRec(t, recA); got != (recEntry{"x", "a", later}) {
		t.Errorf("A recorded %+v, want {x a %v}", got, later)
	}
	if got := recA.count(); got != 3 {
		t.Errorf("A recorded %d entries, want 3", got)
	}
	// B treats it as a normal newer clip: applied, listed with A's ID, not re-broadcast.
	if got := b.clip.Text(); got != "x" {
		t.Errorf("B clipboard = %q, want %q", got, "x")
	}
	if got := lastRec(t, recB); got.text != "x" || got.from != "a" {
		t.Errorf("B recorded %+v, want {x a}", got)
	}
	b.engine.HandleLocal(clip.Item{Text: "x"}) // B's echo
	if got := b.sentCount(); got != 0 {
		t.Errorf("B re-broadcast the restored clip: sent=%d", got)
	}
	if errs := b.errors(); len(errs) != 0 {
		t.Errorf("B errors: %v", errs)
	}
}

func TestRECENT5_RestoreUsesLineEnding(t *testing.T) {
	n := newNet()
	a := n.add(t, "a", lineEnding("\r\n"))
	b := n.add(t, "b")
	if err := a.engine.Restore("l1\nl2"); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := a.clip.Text(); got != "l1\r\nl2" {
		t.Errorf("A clipboard = %q, want CRLF line endings", got)
	}
	if got := lastSent(t, a).Text; got != "l1\nl2" {
		t.Errorf("sent text = %q, want normalized", got)
	}
	a.engine.HandleLocal(clip.Item{Text: "l1\r\nl2"}) // echo with native line endings
	if got := a.sentCount(); got != 1 {
		t.Errorf("A sent %d, want 1", got)
	}
	if got := b.clip.Text(); got != "l1\nl2" {
		t.Errorf("B clipboard = %q", got)
	}
}

func TestRECENT5_ManualCopyAfterRestoreStillSyncs(t *testing.T) {
	n := newNet()
	a := n.add(t, "a")
	b := n.add(t, "b")
	a.engine.HandleLocal(clip.Item{Text: "x"})
	a.engine.HandleLocal(clip.Item{Text: "y"})
	if err := a.engine.Restore("x"); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	a.engine.HandleLocal(clip.Item{Text: "x"}) // echo of the restore
	if got := a.sentCount(); got != 3 {
		t.Fatalf("A sent %d, want 3", got)
	}
	// SYNC-5: copying "y" and then "x" again by hand still syncs both.
	a.engine.HandleLocal(clip.Item{Text: "y"})
	if got := b.clip.Text(); got != "y" {
		t.Fatalf("B clipboard = %q, want y", got)
	}
	a.engine.HandleLocal(clip.Item{Text: "x"})
	if got := b.clip.Text(); got != "x" {
		t.Errorf("B clipboard = %q, want x", got)
	}
	if got := a.sentCount(); got != 5 {
		t.Errorf("A sent %d, want 5", got)
	}
}

func TestRECENT6_RestoringJustReceivedClipStillSyncs(t *testing.T) {
	fixed := time.UnixMilli(1_790_000_000_000) // same ms everywhere: exercises the SentAt bump
	now := func() time.Time { return fixed }
	n := newNet()
	recB, recC := &fakeRec{}, &fakeRec{}
	a := n.add(t, "a", clock(now))
	b := n.add(t, "b", clock(now), withRecent(recB))
	c := n.add(t, "c", clock(now), withRecent(recC))
	c.engine.SetPaused(true)
	c.clip.Copy("older", false)

	a.engine.HandleLocal(clip.Item{Text: "one"})
	if got := b.clip.Text(); got != "one" {
		t.Fatalf("B clipboard = %q, want one", got)
	}
	b.engine.HandleLocal(clip.Item{Text: "one"}) // B's echo of the apply
	if got := c.clip.Text(); got != "older" {
		t.Fatalf("paused C applied %q", got)
	}
	c.engine.SetPaused(false)

	if err := b.engine.Restore("one"); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	b.engine.HandleLocal(clip.Item{Text: "one"}) // echo of the restore's Write
	if got := c.clip.Text(); got != "one" {
		t.Errorf("C clipboard = %q, want one", got)
	}
	if got := b.sentCount(); got != 1 {
		t.Errorf("B sent %d times for the click, want exactly 1", got)
	}
	if got := lastSent(t, b); got.Text != "one" || got.From != "b" {
		t.Errorf("B sent {%q %q}, want {one b}", got.Text, got.From)
	}
	// A and C send nothing after receiving it.
	a.engine.HandleLocal(clip.Item{Text: "one"})
	c.engine.HandleLocal(clip.Item{Text: "one"})
	if got := a.sentCount(); got != 1 {
		t.Errorf("A sent %d, want 1 (its own copy only)", got)
	}
	if got := c.sentCount(); got != 0 {
		t.Errorf("C sent %d, want 0", got)
	}
	if got := lastRec(t, recC); got.text != "one" || got.from != "b" {
		t.Errorf("C recorded %+v, want {one b}", got)
	}
	// B listed "one" from A when it arrived; the restore lists it as this device, now.
	if got := recB.entries(); len(got) != 2 || got[0] != (recEntry{"one", "a", fixed}) || got[1] != (recEntry{"one", "b", fixed}) {
		t.Errorf("B recorded %+v, want [{one a} {one b}] at %v", got, fixed)
	}
	for _, d := range []*device{a, b, c} {
		if errs := d.errors(); len(errs) != 0 {
			t.Errorf("%s errors: %v", d.id, errs)
		}
	}
}

// The restore must be newer than the clip it restores, even when the restorer sorts
// first on the SYNC-6 tie-break or its clock is behind the original sender's.
func TestRECENT6_RestoreOfReceivedClipBeatsItForEveryRestorer(t *testing.T) {
	t0 := time.UnixMilli(1_790_000_000_000)
	cases := []struct {
		name       string
		restorerAt time.Time // the restorer's clock; the sender's is t0
	}{
		{"lower ID same millisecond", t0},
		{"lower ID clock 2s behind", t0.Add(-2 * time.Second)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			senderNow := func() time.Time { return t0 }
			restorerNow := func() time.Time { return tc.restorerAt }
			n := newNet()
			recZ, recC := &fakeRec{}, &fakeRec{}
			a := n.add(t, "a", clock(restorerNow)) // restorer: lower ID than the sender
			z := n.add(t, "z", clock(senderNow), withRecent(recZ))
			c := n.add(t, "c", clock(senderNow), withRecent(recC))
			c.engine.SetPaused(true)
			c.clip.Copy("older", false)

			z.engine.HandleLocal(clip.Item{Text: "one"})
			if got := a.clip.Text(); got != "one" {
				t.Fatalf("A clipboard = %q, want one", got)
			}
			received := lastSent(t, z)
			c.engine.SetPaused(false)

			if err := a.engine.Restore("one"); err != nil {
				t.Fatalf("Restore: %v", err)
			}
			if got := lastSent(t, a).SentAt; !got.After(received.SentAt) {
				t.Errorf("restore SentAt %v not after the received clip's %v", got, received.SentAt)
			}
			if got := c.clip.Text(); got != "one" {
				t.Errorf("C clipboard = %q, want one (restore dropped as older?)", got)
			}
			if got := lastRec(t, recC); got.text != "one" || got.from != "a" {
				t.Errorf("C recorded %+v, want {one a}", got)
			}
			if got := lastRec(t, recZ); got.text != "one" || got.from != "a" {
				t.Errorf("Z recorded %+v, want {one a} (restore dropped as older?)", got)
			}
		})
	}
}

// failingClip is a clipboard whose Write always fails, e.g. another app holds it open.
type failingClip struct{ *clip.Fake }

func (failingClip) Write(string) error { return errors.New("clipboard busy") }

func TestRECENT5_RestoreFailedWriteSendsNothing(t *testing.T) {
	n := newNet()
	rec := &fakeRec{}
	a := n.add(t, "a", withRecent(rec), func(o *Options) { o.Clipboard = failingClip{clip.NewFake()} })
	b := n.add(t, "b")
	a.engine.HandleLocal(clip.Item{Text: "x"})
	a.engine.HandleLocal(clip.Item{Text: "y"})
	if err := a.engine.Restore("x"); err == nil {
		t.Fatal("Restore returned nil, want the Write error")
	}
	if got := a.sentCount(); got != 2 {
		t.Errorf("A sent %d after a failed restore, want 2", got)
	}
	if got := rec.count(); got != 2 {
		t.Errorf("A recorded %d after a failed restore, want 2", got)
	}
	// Nothing was written, so there is no echo: copying "x" by hand must still sync.
	a.engine.HandleLocal(clip.Item{Text: "x"})
	if got := a.sentCount(); got != 3 {
		t.Errorf("manual copy after a failed restore not sent: sent=%d, want 3", got)
	}
	if got := b.clip.Text(); got != "x" {
		t.Errorf("B clipboard = %q, want x", got)
	}
}

func TestRECENT7_RestoreWhilePausedIsLocalOnly(t *testing.T) {
	t0 := time.UnixMilli(1_790_000_000_000)
	ck := &stepClock{cur: t0}
	n := newNet()
	recA, recB := &fakeRec{}, &fakeRec{}
	a := n.add(t, "a", clock(ck.now), withRecent(recA))
	b := n.add(t, "b", clock(ck.now), withRecent(recB))
	a.engine.HandleLocal(clip.Item{Text: "x"})
	a.engine.HandleLocal(clip.Item{Text: "y"})
	a.engine.SetPaused(true)

	later := t0.Add(7 * time.Second)
	ck.set(later)
	if err := a.engine.Restore("x"); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := a.clip.Text(); got != "x" {
		t.Errorf("A clipboard = %q, want x", got)
	}
	if got := a.sentCount(); got != 2 {
		t.Errorf("paused A sent %d, want 2 (nothing for the restore)", got)
	}
	if got := b.clip.Text(); got != "y" {
		t.Errorf("B clipboard = %q, want y", got)
	}
	// Listed first as "this device — just now" (decision 2).
	if got := lastRec(t, recA); got != (recEntry{"x", "a", later}) {
		t.Errorf("A recorded %+v, want {x a %v}", got, later)
	}
	if got := recA.count(); got != 3 {
		t.Errorf("A recorded %d entries, want 3", got)
	}
	if got := recB.count(); got != 2 {
		t.Errorf("B recorded %d entries, want 2", got)
	}
	// The restore's clipboard change is not sent after resuming either, even when its
	// notification only arrives after the resume.
	a.engine.SetPaused(false)
	a.engine.HandleLocal(clip.Item{Text: "x"})
	if got := a.sentCount(); got != 2 {
		t.Errorf("A sent the paused restore after resuming: sent=%d, want 2", got)
	}
	if got := b.clip.Text(); got != "y" {
		t.Errorf("B clipboard = %q, want y", got)
	}
}

func TestRECENT7_PausedRestoreOfReceivedClipIsThisDevice(t *testing.T) {
	t0 := time.UnixMilli(1_790_000_000_000)
	ck := &stepClock{cur: t0}
	n := newNet()
	recA := &fakeRec{}
	a := n.add(t, "a", clock(ck.now), withRecent(recA))
	b := n.add(t, "b", clock(ck.now))
	b.engine.HandleLocal(clip.Item{Text: "x"}) // A applies it, listed from B
	a.engine.SetPaused(true)
	later := t0.Add(3 * time.Second)
	ck.set(later)
	if err := a.engine.Restore("x"); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := recA.entries(); len(got) != 2 || got[1] != (recEntry{"x", "a", later}) {
		t.Errorf("A recorded %+v, want second entry {x a %v}", got, later)
	}
	if got := a.sentCount(); got != 0 {
		t.Errorf("paused A sent %d, want 0", got)
	}
}

// Human decision at gate 3 (2026-10-19): no passphrase wins over paused. The restore
// writes the clipboard only; nothing is recorded or sent (SEC-4, RECENT-16).
func TestRECENT7_RestoreWhilePausedWithoutPassphraseRecordsNothing(t *testing.T) {
	n := newNet()
	rec := &fakeRec{}
	a := n.add(t, "a", withKey(nil), withRecent(rec))
	b := n.add(t, "b")
	a.engine.SetPaused(true)
	if err := a.engine.Restore("x"); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := a.clip.Text(); got != "x" {
		t.Errorf("A clipboard = %q, want x", got)
	}
	if rec.count() != 0 {
		t.Errorf("paused restore without passphrase recorded %d entries, want 0", rec.count())
	}
	if a.sentCount() != 0 || b.clip.Text() != "" {
		t.Errorf("A sent: sent=%d Bclip=%q", a.sentCount(), b.clip.Text())
	}
	if !a.hasEvent(EventNoPassphrase) {
		t.Error("no EventNoPassphrase emitted")
	}
	// Neither resuming nor setting a passphrase sends the restore's echo.
	a.engine.SetPaused(false)
	a.engine.SetKey(&keyAlpha)
	a.engine.HandleLocal(clip.Item{Text: "x"})
	if a.sentCount() != 0 {
		t.Errorf("echo of the restore sent later: %d", a.sentCount())
	}
}

func TestRECENT7_RestoreWithoutPassphraseIsLocalOnly(t *testing.T) {
	n := newNet()
	rec := &fakeRec{}
	a := n.add(t, "a", withKey(nil), withRecent(rec))
	b := n.add(t, "b")
	if err := a.engine.Restore("x"); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := a.clip.Text(); got != "x" {
		t.Errorf("A clipboard = %q, want x", got)
	}
	if a.sentCount() != 0 || b.clip.Text() != "" {
		t.Errorf("A without passphrase sent: sent=%d Bclip=%q", a.sentCount(), b.clip.Text())
	}
	if rec.count() != 0 {
		t.Errorf("unsynced restore recorded (SEC-4, RECENT-16): %d", rec.count())
	}
	// Setting a passphrase later must not send the restore's echo.
	a.engine.SetKey(&keyAlpha)
	a.engine.HandleLocal(clip.Item{Text: "x"})
	if a.sentCount() != 0 {
		t.Errorf("echo of the restore sent after setting a passphrase: %d", a.sentCount())
	}
}

// --- Reviewer note (PR #24): the Recent order follows the clipboard ---

// hookNet is a Broadcaster that runs a hook on the first Broadcast. The engine calls
// Broadcast after releasing its lock, so the hook slips a Receive in between a local
// send and whatever the engine does after Broadcast, deterministically.
type hookNet struct{ hook func() }

func (h *hookNet) Broadcast([]byte) {
	if f := h.hook; f != nil {
		h.hook = nil
		f()
	}
}

// raceWithNewerReceive returns engine "a" whose next Broadcast is overtaken by a newer
// clip from "b". That clip wins (SYNC-6) and ends on A's clipboard.
func raceWithNewerReceive(t *testing.T) (*Engine, *clip.Fake, *fakeRec, *error) {
	t.Helper()
	fixed := time.UnixMilli(1_790_000_000_000)
	c := clip.NewFake()
	rec := &fakeRec{}
	hn := &hookNet{}
	e := New(Options{DeviceID: "a", Clipboard: c, Network: hn, Key: &keyAlpha,
		Now: func() time.Time { return fixed }, Recent: rec})
	sealedB, err := proto.Seal(keyAlpha, proto.Message{ID: proto.NewID(), From: "b",
		SentAt: fixed.Add(time.Second), Text: "from-b"})
	if err != nil {
		t.Fatal(err)
	}
	recvErr := errors.New("receive never ran")
	hn.hook = func() { recvErr = e.Receive(sealedB) }
	return e, c, rec, &recvErr
}

func checkRecentMatchesClipboard(t *testing.T, c *clip.Fake, rec *fakeRec, recvErr error) {
	t.Helper()
	if recvErr != nil {
		t.Fatalf("Receive: %v", recvErr)
	}
	if c.Text() != "from-b" {
		t.Fatalf("clipboard = %q, want the newer clip from-b", c.Text())
	}
	if got := lastRec(t, rec); got.text != "from-b" {
		t.Fatalf("newest Recent entry = %q, but the clipboard holds %q: %+v", got.text, c.Text(), rec.entries())
	}
	if rec.count() != 2 {
		t.Fatalf("recorded %d entries, want 2: %+v", rec.count(), rec.entries())
	}
}

func TestRECENT1_SYNC6_SendRacingNewerReceiveKeepsListOrder(t *testing.T) {
	e, c, rec, recvErr := raceWithNewerReceive(t)
	c.Copy("from-a", false)
	e.HandleLocal(clip.Item{Text: "from-a"})
	checkRecentMatchesClipboard(t, c, rec, *recvErr)
}

func TestRECENT5_SYNC6_RestoreRacingNewerReceiveKeepsListOrder(t *testing.T) {
	e, c, rec, recvErr := raceWithNewerReceive(t)
	if err := e.Restore("restored"); err != nil {
		t.Fatal(err)
	}
	checkRecentMatchesClipboard(t, c, rec, *recvErr)
}

// lockRec is a Recorder that fails the test if Record runs without e.mu held. Record
// must run in the same critical section that decides the SYNC-6 order, or a racing
// Receive can slip in between and the list no longer matches the clipboard. The tests
// using it run on one goroutine, so TryLock only fails because the engine holds e.mu.
type lockRec struct {
	t *testing.T
	e *Engine // set after the engine is created
	fakeRec
}

func (r *lockRec) Record(text, from string, at time.Time) {
	if r.e.mu.TryLock() {
		r.e.mu.Unlock()
		r.t.Errorf("Record(%q, from %q) called without the engine lock held", text, from)
	}
	r.fakeRec.Record(text, from, at)
}

func TestRECENT1_RecordRunsUnderEngineLock(t *testing.T) {
	n := newNet()
	recA, recB := &lockRec{t: t}, &lockRec{t: t}
	a := n.add(t, "a", withRecent(recA))
	b := n.add(t, "b", withRecent(recB))
	recA.e, recB.e = a.engine, b.engine

	a.engine.HandleLocal(clip.Item{Text: "sent"}) // A records a send, B a receive
	if err := a.engine.Restore("restored"); err != nil {
		t.Fatal(err)
	}
	a.engine.SetPaused(true)
	if err := a.engine.Restore("paused restore"); err != nil {
		t.Fatal(err)
	}
	if recA.count() != 3 || recB.count() != 2 {
		t.Fatalf("recorded A=%d B=%d, want 3 and 2", recA.count(), recB.count())
	}
}

// RECENT-13: a clip decrypted with the old passphrase, and applied only after the
// passphrase changed (a race in Receive), is dropped: it is neither written nor listed.
func TestRECENT13_ClipOpenedWithOldKeyDroppedAfterKeyChange(t *testing.T) {
	n := newNet()
	rec := &fakeRec{}
	a := n.add(t, "a", withRecent(rec))
	seal := func(k proto.Key, text string, at time.Time) []byte {
		t.Helper()
		s, err := proto.Seal(k, proto.Message{ID: proto.NewID(), From: "b", SentAt: at, Text: text})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	now := time.Now()

	m, gen, err := a.engine.open(seal(keyAlpha, "old passphrase", now)) // Receive's first half
	if err != nil {
		t.Fatal(err)
	}
	a.engine.SetKey(&keyBravo) // the passphrase changes before the second half
	if err := a.engine.apply(m, gen); !errors.Is(err, ErrKeyChanged) {
		t.Fatalf("apply after key change = %v, want ErrKeyChanged", err)
	}
	if a.clip.Writes() != 0 || rec.count() != 0 {
		t.Fatalf("old-key clip applied: clipboard %q, recorded %+v", a.clip.Text(), rec.entries())
	}

	// Setting the same passphrase again also counts as a change.
	m, gen, err = a.engine.open(seal(keyBravo, "same key", now.Add(time.Millisecond)))
	if err != nil {
		t.Fatal(err)
	}
	a.engine.SetKey(&keyBravo)
	if err := a.engine.apply(m, gen); !errors.Is(err, ErrKeyChanged) {
		t.Fatalf("apply after re-setting the key = %v, want ErrKeyChanged", err)
	}

	// Without a change in between, a clip applies as usual.
	if err := a.engine.Receive(seal(keyBravo, "new passphrase", now.Add(2*time.Millisecond))); err != nil {
		t.Fatal(err)
	}
	if a.clip.Text() != "new passphrase" || rec.count() != 1 {
		t.Fatalf("clip under the new key: clipboard %q, recorded %d", a.clip.Text(), rec.count())
	}
}
