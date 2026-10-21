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
