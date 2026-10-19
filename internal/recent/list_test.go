package recent

import (
	"sync"
	"testing"
	"time"
)

// texts returns the Entry.Text values of es, in order.
func texts(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Text
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// [RECENT-3] Newest first, at most 5 entries: c1..c6 gives c6, c5, c4, c3, c2.
func TestRECENT3_NewestFirstAtMostFive(t *testing.T) {
	var l List
	t0 := time.Now()
	for i, text := range []string{"c1", "c2", "c3", "c4", "c5", "c6"} {
		l.Record(text, "me", t0.Add(time.Duration(i)*time.Second))
	}
	got := texts(l.Entries())
	want := []string{"c6", "c5", "c4", "c3", "c2"}
	if !equal(got, want) {
		t.Fatalf("RECENT-3: got %q, want %q", got, want)
	}
}

// [RECENT-4] The same text is listed once, at the top, with the new source and time.
func TestRECENT4_SameTextListedOnceAtTop(t *testing.T) {
	var l List
	t0 := time.Now()
	l.Record("x", "a", t0)
	l.Record("y", "a", t0.Add(time.Second))
	l.Record("x", "b", t0.Add(2*time.Second))

	got := texts(l.Entries())
	want := []string{"x", "y"}
	if !equal(got, want) {
		t.Fatalf("RECENT-4: got %q, want %q", got, want)
	}
	top := l.Entries()[0]
	if top.From != "b" || !top.At.Equal(t0.Add(2*time.Second)) {
		t.Fatalf("RECENT-4: re-recorded entry kept old source/time: %+v", top)
	}
}

// [RECENT-9] The source name is resolved once, at record time, and stored.
func TestRECENT9_NameStoredAtRecordTime(t *testing.T) {
	var l List
	l.NameOf = func(id string) string {
		if id == "peer1" {
			return "Mac"
		}
		return ""
	}
	t0 := time.Now()
	l.Record("x", "peer1", t0)
	l.Record("y", "peer2", t0)

	// Changing the resolver afterwards: new records use it, stored names do not.
	l.NameOf = func(string) string { return "Changed" }
	l.Record("w", "peer1", t0)

	es := l.Entries() // w, y, x
	if es[0].Name != "Changed" {
		t.Fatalf("RECENT-9: Record must use the NameOf current at record time: %q", es[0].Name)
	}
	if es[1].Name != "" || es[2].Name != "Mac" {
		t.Fatalf("RECENT-9: stored Names must not be re-looked-up: %q %q", es[1].Name, es[2].Name)
	}

	// Unknown name (nil resolver) stores "".
	var l2 List
	l2.Record("z", "peer3", t0)
	if got := l2.Entries()[0].Name; got != "" {
		t.Fatalf("RECENT-9: nil NameOf must store empty Name, got %q", got)
	}
}

// [RECENT-11] enabling piece: a new list is empty.
func TestRECENT11_NewListIsEmpty(t *testing.T) {
	var l List
	es := l.Entries()
	if len(es) != 0 {
		t.Fatalf("RECENT-11: new list not empty: %+v", es)
	}
	if es == nil {
		t.Fatal("RECENT-11: Entries() must return an empty slice, not nil")
	}
}

// [RECENT-12] enabling piece: Clear empties the list.
func TestRECENT12_ClearEmptiesList(t *testing.T) {
	var l List
	t0 := time.Now()
	l.Record("x", "a", t0)
	l.Record("y", "a", t0)
	l.Clear()
	if got := l.Entries(); len(got) != 0 {
		t.Fatalf("RECENT-12: after Clear got %d entries", len(got))
	}
	// Recording still works after Clear.
	l.Record("z", "a", t0)
	if got := texts(l.Entries()); !equal(got, []string{"z"}) {
		t.Fatalf("RECENT-12: record after clear got %q", got)
	}
}

// Entries returns a copy: mutating it must not affect the list.
func TestRECENT_EntriesReturnsCopy(t *testing.T) {
	var l List
	t0 := time.Now()
	l.Record("x", "a", t0)
	es := l.Entries()
	es[0].Text = "mutated"
	if got := texts(l.Entries()); !equal(got, []string{"x"}) {
		t.Fatalf("Entries() leaked internal state: %q", got)
	}
}

// List must be safe for concurrent use (run with -race).
func TestRECENT_ConcurrentRecordEntriesClear(t *testing.T) {
	l := &List{NameOf: func(string) string { return "n" }}
	t0 := time.Now()
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 200 {
				l.Record(string(rune('a'+i%26)), "peer", t0.Add(time.Duration(i)*time.Second))
			}
		}()
	}
	wg.Add(2)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				l.Entries()
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				l.Clear()
			}
		}
	}()
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()
	if n := len(l.Entries()); n > MaxEntries {
		t.Fatalf("list exceeded max size: %d", n)
	}
}
