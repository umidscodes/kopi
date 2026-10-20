package main

import (
	"context"
	"testing"
	"time"

	"github.com/umidscodes/kopi/internal/app"
	"github.com/umidscodes/kopi/internal/clip"
	"github.com/umidscodes/kopi/internal/discovery"
	"github.com/umidscodes/kopi/internal/transport"
)

// newMenuNode starts a real node with a fake clipboard, like the RECENT-13 test.
func newMenuNode(t *testing.T) (*kopiApp, *app.Node, *clip.Fake) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cb := clip.NewFake()
	n, err := app.Start(ctx, app.Options{
		DeviceID: "self", DeviceName: "self", Key: keyFor("pw"), Clipboard: cb,
		ListenAddr: "127.0.0.1:0", Discovery: discovery.NewFakeLAN().Join(transport.Peer{ID: "self"}),
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); n.Wait() })
	return &kopiApp{node: n}, n, cb
}

// drawForTest mimics the snapshot step of redrawRecent (its systray calls need a GUI).
func drawForTest(a *kopiApp, n *app.Node) {
	a.recentDrawn = recentMenu(n.Recent.Entries(), time.Now(), "self")
}

// RECENT-5: a clip arriving after the menu was drawn must not shift the click target.
func TestRECENT5_ClickRestoresDrawnEntryEvenIfListChanged(t *testing.T) {
	a, n, cb := newMenuNode(t)
	n.Engine.HandleLocal(clip.Item{Text: "x"})
	n.Engine.HandleLocal(clip.Item{Text: "y"})
	drawForTest(a, n)                          // slots: y, x
	n.Engine.HandleLocal(clip.Item{Text: "z"}) // arrives, no redraw

	a.restoreSlot(1) // the slot that showed "x"
	if got := cb.Text(); got != "x" {
		t.Fatalf("clipboard after clicking the slot that showed x = %q, want x", got)
	}
}

// RECENT-12: Clear empties the list, leaves the clipboard alone, and a stale click
// afterwards restores nothing.
func TestRECENT12_ClearEmptiesListAndKeepsClipboard(t *testing.T) {
	a, n, cb := newMenuNode(t)
	if err := n.Engine.Restore("x"); err != nil {
		t.Fatal(err)
	}
	writes := cb.Writes()
	drawForTest(a, n)
	if len(a.recentDrawn) != 1 {
		t.Fatalf("drawn = %+v, want [x]", a.recentDrawn)
	}

	a.clearRecent()
	if es := n.Recent.Entries(); len(es) != 0 {
		t.Fatalf("list after Clear = %+v, want empty", es)
	}
	if cb.Text() != "x" || cb.Writes() != writes {
		t.Fatalf("clipboard changed by Clear: text %q, writes %d -> %d", cb.Text(), writes, cb.Writes())
	}
	a.restoreSlot(0) // stale click on the old snapshot
	if cb.Writes() != writes {
		t.Fatal("a click after Clear restored a cleared clip")
	}
	if es := n.Recent.Entries(); len(es) != 0 {
		t.Fatalf("stale click re-recorded %+v", es)
	}
}

// RECENT-13 and the menu: a passphrase change drops the drawn snapshot too.
func TestRECENT13_PassphraseChangeDropsDrawnSnapshot(t *testing.T) {
	a, n, cb := newMenuNode(t)
	n.Engine.HandleLocal(clip.Item{Text: "x"})
	drawForTest(a, n)
	a.applyPassphrase("other")
	writes := cb.Writes()
	a.restoreSlot(0)
	if cb.Writes() != writes {
		t.Fatal("a click after a passphrase change restored a cleared clip")
	}
}

// RECENT-11: what the submenu items look like.
func TestRECENT11_MenuStateEmptyAndFilled(t *testing.T) {
	empty := recentMenuState(nil)
	if !empty.ShowNone || empty.ClearEnabled {
		t.Fatalf("empty state = %+v, want No recent clips shown and Clear disabled", empty)
	}
	for i, v := range empty.SlotVisible {
		if v {
			t.Errorf("empty: slot %d visible", i)
		}
	}
	two := recentMenuState([]recentSlot{{}, {}})
	if two.ShowNone || !two.ClearEnabled {
		t.Fatalf("filled state = %+v, want No recent clips hidden and Clear enabled", two)
	}
	for i, v := range two.SlotVisible {
		if v != (i < 2) {
			t.Errorf("two entries: slot %d visible = %v", i, v)
		}
	}
}
