package clip

import (
	"context"
	"sync"
)

// Fake is an in-memory Clipboard for tests. Like a real OS clipboard, Write
// triggers a change notification to watchers.
//
// Each watcher gets the changes in the order they were made, like the polled OS
// clipboards report them: the echo of a Write never overtakes a later Copy or Write.
// The sync engine relies on that order (SYNC-4). Neither Write nor Copy ever blocks:
// changes go into an unbounded queue per watcher, which one goroutine per watcher
// drains into the Watch channel.
//
// Like the real clipboards, the Watch channel is closed once ctx is done, and
// changes not yet delivered by then are dropped. A reader must check the receive's
// ok value: a closed channel yields zero Items forever, never a real change.
type Fake struct {
	mu       sync.Mutex
	text     string
	watchers []*watcher
	writes   int
}

// watcher is one Watch call. queue is guarded by Fake.mu.
type watcher struct {
	out   chan Item
	wake  chan struct{} // capacity 1: "the queue may be non-empty"
	queue []Item
}

var _ Clipboard = (*Fake)(nil)

func NewFake() *Fake { return &Fake{} }

// Copy simulates the user copying text in some app.
func (f *Fake) Copy(text string, sensitive bool) {
	f.mu.Lock()
	f.text = text
	f.notify(Item{Text: text, Sensitive: sensitive})
	f.mu.Unlock()
}

// Write never blocks: the writer (the sync engine) may hold locks while calling it.
func (f *Fake) Write(text string) error {
	f.mu.Lock()
	f.text = text
	f.writes++
	f.notify(Item{Text: text})
	f.mu.Unlock()
	return nil
}

// notify queues it for every watcher. f.mu must be held, so each queue follows the
// order in which the clipboard changed.
func (f *Fake) notify(it Item) {
	for _, w := range f.watchers {
		w.queue = append(w.queue, it)
		select {
		case w.wake <- struct{}{}:
		default: // already signalled
		}
	}
}

func (f *Fake) Watch(ctx context.Context) (<-chan Item, error) {
	w := &watcher{out: make(chan Item), wake: make(chan struct{}, 1)}
	f.mu.Lock()
	f.watchers = append(f.watchers, w)
	f.mu.Unlock()
	go f.deliver(ctx, w)
	return w.out, nil
}

// deliver hands w's queued changes to its channel, oldest first, until ctx is done,
// then closes the channel. It never holds f.mu while sending, so a slow reader can't
// block Write. Only deliver sends on or closes w.out, so it is closed exactly once
// and never sent on after.
func (f *Fake) deliver(ctx context.Context, w *watcher) {
	defer close(w.out) // runs after unwatch: Write stops queueing for w first
	defer f.unwatch(w)
	for {
		f.mu.Lock()
		if len(w.queue) == 0 {
			f.mu.Unlock()
			select {
			case <-w.wake:
				continue
			case <-ctx.Done():
				return
			}
		}
		it := w.queue[0]
		w.queue = w.queue[1:]
		f.mu.Unlock()
		select {
		case w.out <- it:
		case <-ctx.Done():
			return
		}
	}
}

func (f *Fake) unwatch(w *watcher) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, x := range f.watchers {
		if x == w {
			f.watchers = append(f.watchers[:i], f.watchers[i+1:]...)
			return
		}
	}
}

// Text returns the current clipboard contents.
func (f *Fake) Text() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.text
}

// Writes returns how many times Write was called (i.e. clips applied from peers).
func (f *Fake) Writes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes
}
