package main

import (
	"context"
	"testing"
	"time"

	"github.com/umidscodes/kopi/internal/app"
	"github.com/umidscodes/kopi/internal/clip"
	"github.com/umidscodes/kopi/internal/discovery"
	ksync "github.com/umidscodes/kopi/internal/sync"
	"github.com/umidscodes/kopi/internal/transport"
)

// RECENT-10 (refresh wiring): a sent or applied clip marks the menu dirty so the
// refresher redraws the list (Record runs before the event is emitted).
func TestRECENT10_SentAndAppliedEventsMarkMenuDirty(t *testing.T) {
	for _, kind := range []ksync.EventKind{ksync.EventSent, ksync.EventApplied} {
		a := &kopiApp{dirty: make(chan struct{}, 1)} // markDirty never blocks
		a.onEvent(ksync.Event{Kind: kind, Size: 3})
		select {
		case <-a.dirty:
		default:
			t.Errorf("event %d did not mark the menu dirty", kind)
		}
	}
}

// RECENT-10: the refresher redraws on the refresh tick (the 60 s age-label timer,
// here driven by the test) as well as on dirty signals.
func TestRECENT10_RefresherRedrawsOnTickAndDirty(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &kopiApp{ctx: ctx, dirty: make(chan struct{}, 1)}
	ticks := make(chan time.Time)
	redraws := make(chan struct{}, 4)
	go a.refreshLoop(ticks, func() { redraws <- struct{}{} })

	for _, why := range []string{"tick", "dirty"} {
		if why == "tick" {
			ticks <- time.Now()
		} else {
			a.markDirty()
		}
		select {
		case <-redraws:
		case <-time.After(2 * time.Second):
			t.Fatalf("%s did not trigger a redraw", why)
		}
	}
}

// RECENT-10 (shutdown): cancelling the app context must end refreshLoop; without
// that branch the refresher goroutine leaks on quit.
func TestRECENT10_RefresherExitsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	a := &kopiApp{ctx: ctx, dirty: make(chan struct{}, 1)}
	ticks := make(chan time.Time)
	done := make(chan struct{})
	go func() {
		a.refreshLoop(ticks, func() {})
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("refreshLoop did not return within 2s of context cancel; the refresher leaked")
	}
}

// RECENT-13: both the tray dialog and a kopi.yml edit go through applyPassphrase,
// which must empty the Recent clips list.
func TestRECENT13_ApplyPassphraseEmptiesList(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	n, err := app.Start(ctx, app.Options{
		DeviceID: "a", DeviceName: "a", Key: keyFor("old"), Clipboard: clip.NewFake(),
		ListenAddr: "127.0.0.1:0", Discovery: discovery.NewFakeLAN().Join(transport.Peer{ID: "a"}),
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); n.Wait() })

	n.Engine.HandleLocal(clip.Item{Text: "x"})
	if es := n.Recent.Entries(); len(es) != 1 {
		t.Fatalf("list before the change = %+v, want [x]", es)
	}

	a := &kopiApp{node: n} // markDirty never blocks, even without a refresher
	a.applyPassphrase("new")
	if es := n.Recent.Entries(); len(es) != 0 {
		t.Fatalf("list after applyPassphrase = %+v, want empty", es)
	}
	if !n.Engine.HasKey() {
		t.Fatal("no key after applyPassphrase")
	}
}
