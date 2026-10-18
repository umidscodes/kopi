package clip

import (
	"context"
	"sync"
)

// Fake is an in-memory Clipboard for tests. Like a real OS clipboard, Write
// triggers a change notification to watchers.
type Fake struct {
	mu       sync.Mutex
	text     string
	watchers []chan Item
	writes   int
}

var _ Clipboard = (*Fake)(nil)

func NewFake() *Fake { return &Fake{} }

// Copy simulates the user copying text in some app.
func (f *Fake) Copy(text string, sensitive bool) {
	f.mu.Lock()
	f.text = text
	ws := append([]chan Item(nil), f.watchers...)
	f.mu.Unlock()
	for _, w := range ws {
		w <- Item{Text: text, Sensitive: sensitive}
	}
}

func (f *Fake) Write(text string) error {
	f.mu.Lock()
	f.text = text
	f.writes++
	ws := append([]chan Item(nil), f.watchers...)
	f.mu.Unlock()
	// Deliver asynchronously: a real OS notifies watchers later, and the writer
	// (the sync engine) may hold locks while calling Write.
	for _, w := range ws {
		go func(w chan Item) { w <- Item{Text: text} }(w)
	}
	return nil
}

func (f *Fake) Watch(ctx context.Context) (<-chan Item, error) {
	ch := make(chan Item, 64)
	f.mu.Lock()
	f.watchers = append(f.watchers, ch)
	f.mu.Unlock()
	go func() {
		<-ctx.Done()
		f.mu.Lock()
		for i, w := range f.watchers {
			if w == ch {
				f.watchers = append(f.watchers[:i], f.watchers[i+1:]...)
				break
			}
		}
		f.mu.Unlock()
	}()
	return ch, nil
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
