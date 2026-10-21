package clip

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// next receives one change from ch or fails the test.
func next(t *testing.T, ch <-chan Item) Item {
	t.Helper()
	select {
	case it := <-ch:
		return it
	case <-time.After(5 * time.Second):
		t.Fatal("no clipboard change delivered")
		return Item{}
	}
}

// The sync engine relies on changes arriving in the order they happened, like the
// polled OS clipboards report them: an echo of a Write must never overtake a later
// Copy (SYNC-4). Fake guarantees that order per watcher.
func TestSYNC4_FakeDeliversChangesInOrder(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := NewFake()
	ch1, _ := f.Watch(ctx)
	ch2, _ := f.Watch(ctx)

	const n = 40 // nobody reads until all changes are made
	for i := 0; i < n; i++ {
		if i%2 == 0 {
			f.Write(fmt.Sprint(i))
		} else {
			f.Copy(fmt.Sprint(i), i%3 == 0)
		}
	}
	for _, ch := range []<-chan Item{ch1, ch2} {
		for i := 0; i < n; i++ {
			it := next(t, ch)
			if it.Text != fmt.Sprint(i) {
				t.Fatalf("change %d: got %q, want %q", i, it.Text, fmt.Sprint(i))
			}
			if want := i%2 == 1 && i%3 == 0; it.Sensitive != want {
				t.Fatalf("change %d: sensitive = %v, want %v", i, it.Sensitive, want)
			}
		}
	}
}

// The engine calls Write with its lock held, so Write must return even when the
// watcher is not reading. Copy doesn't block either: the queue has no fixed size.
func TestFakeWriteNeverBlocks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := NewFake()
	f.Watch(ctx) // never read

	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			f.Write("x")
			f.Copy("y", false)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Write or Copy blocked on a watcher that is not reading")
	}
	if got := f.Writes(); got != 1000 {
		t.Fatalf("Writes() = %d, want 1000", got)
	}
}

// The real clipboards close their Watch channel when ctx ends (defer close(ch)).
// Fake must do the same, or tests can't see a reader that mishandles the closed
// channel (issue #33). Changes still queued when ctx ends may be dropped, like a
// real watcher drops them; Write and Copy keep working and never block.
func TestFakeClosesWatchChannelWhenCtxEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := NewFake()
	ch1, _ := f.Watch(ctx)
	ch2, _ := f.Watch(ctx)
	f.Copy("queued 1", false) // never read before cancel
	f.Write("queued 2")
	cancel()

	for name, ch := range map[string]<-chan Item{"watcher 1": ch1, "watcher 2": ch2} {
		timeout := time.After(5 * time.Second)
	drain:
		for {
			select {
			case it, ok := <-ch:
				if !ok {
					break drain
				}
				if it.Text != "queued 1" && it.Text != "queued 2" {
					t.Fatalf("%s: got %q after cancel, want a queued change or close", name, it.Text)
				}
			case <-timeout:
				t.Fatalf("%s: channel not closed after ctx ended", name)
			}
		}
	}

	done := make(chan struct{})
	go func() {
		f.Write("after")
		f.Copy("after", false)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Write or Copy blocked after the watchers stopped")
	}
	if got := f.Text(); got != "after" {
		t.Fatalf("Text() = %q, want %q", got, "after")
	}
}
