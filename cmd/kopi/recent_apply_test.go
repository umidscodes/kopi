package main

import (
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/umidscodes/kopi/internal/clip"
)

type fakeItem struct {
	title            string
	visible, enabled bool
}

func (f *fakeItem) SetTitle(s string) { f.title = s }
func (f *fakeItem) Show()             { f.visible = true }
func (f *fakeItem) Hide()             { f.visible = false }
func (f *fakeItem) Enable()           { f.enabled = true }
func (f *fakeItem) Disable()          { f.enabled = false }

func apply(slots []recentSlot) (items [recentSlots]*fakeItem, none, clr *fakeItem) {
	var mi [recentSlots]menuItem
	for i := range items {
		items[i] = &fakeItem{visible: true} // start from a stale, fully drawn menu
		mi[i] = items[i]
	}
	none, clr = &fakeItem{}, &fakeItem{enabled: true}
	applyRecentMenu(slots, recentMenuState(slots), mi, none, clr)
	return
}

// RECENT-11: the empty list shows "No recent clips" and disables Clear; a filled list
// shows its slots and enables Clear.
func TestRECENT11_ApplyEmptyAndFilledMenu(t *testing.T) {
	items, none, clr := apply(nil)
	if !none.visible || clr.enabled {
		t.Fatalf("empty: none visible=%v clear enabled=%v, want true,false", none.visible, clr.enabled)
	}
	for i, it := range items {
		if it.visible {
			t.Errorf("empty: slot %d still visible", i)
		}
	}

	items, none, clr = apply([]recentSlot{{Label: "a"}, {Label: "b"}})
	if none.visible || !clr.enabled {
		t.Fatalf("filled: none visible=%v clear enabled=%v, want false,true", none.visible, clr.enabled)
	}
	if !items[0].visible || items[0].title != "a" || !items[1].visible || items[1].title != "b" || items[2].visible {
		t.Fatalf("filled slots wrong: %+v %+v %+v", *items[0], *items[1], *items[2])
	}
}

// RECENT-5 (smoke test): clicking, clearing, snapshotting and engine recording from
// several goroutines must not deadlock. This only shows a deadlock by failing; the
// lock rules (recentMu is a leaf lock, held only for the store, never across a call
// into the node) are the real guard against a lock-order inversion.
func TestRECENT5_ConcurrentClickClearSnapshotDoNotDeadlock(t *testing.T) {
	a, n, _ := newMenuNode(t)
	start := make(chan struct{}) // barrier: all four goroutines begin together
	// Shared deadline, published before the barrier opens (the channel close gives
	// the happens-before edge): each goroutine hammers until ~300 ms after start,
	// so the test tries many more interleavings than a fixed iteration count.
	deadline := time.Now().Add(300 * time.Millisecond)
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for i := 0; time.Now().Before(deadline); i++ {
				switch g {
				case 0:
					n.Engine.HandleLocal(clip.Item{Text: string(rune('a' + i%26))})
				case 1:
					a.snapshotRecent(n, "self")
				case 2:
					a.restoreSlot(i % recentSlots)
				default:
					a.clearRecent()
				}
			}
		}(g)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	close(start)
	select {
	case <-done:
	case <-time.After(5 * time.Second): // watchdog: a lock-order inversion is a named failure, not a 10-minute hang
		buf := make([]byte, 1<<20)
		nb := runtime.Stack(buf, true)
		t.Fatalf("deadlock: click/clear/snapshot did not finish in 5s, goroutine dump:\n%s", buf[:nb])
	}
}
