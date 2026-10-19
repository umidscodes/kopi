// Package recent keeps an in-memory list of the last synced clips, newest
// first, for the tray's "Recent clips" menu. Nothing here touches disk or
// the network, and no clip text is ever logged.
package recent

import (
	"slices"
	"sync"
	"time"
)

// MaxEntries is the number of clips the list remembers (RECENT-3).
const MaxEntries = 5

// Entry is one recorded clip.
//
// Name is the display name of the source device, resolved once at record
// time from NameOf; it is never looked up again (RECENT-9, so a menu draw
// cannot race the discovery cache).
type Entry struct {
	Text string
	From string // source device ID
	Name string // source display name, resolved at record time (may be "")
	At   time.Time
}

// List is a bounded, deduplicating, newest-first clip list, safe for
// concurrent use.
type List struct {
	// NameOf maps a device ID to its display name; "" when unknown. Set
	// once by the creator before use, and before the list is shared with
	// other goroutines; Record reads it without holding mu.
	NameOf func(id string) string

	mu sync.Mutex
	e  []Entry
}

// Record puts a clip at the top of the list. An entry with identical text is
// removed first, so each text appears once, at the top, with the new source
// and time (RECENT-4). The oldest entry falls off beyond MaxEntries
// (RECENT-3). The signature matches the sync.Recorder interface (#11).
func (l *List) Record(text, from string, at time.Time) {
	// Resolve the name before taking mu: a NameOf implementation that
	// takes its own lock (e.g. a discovery-cache lookup) must never be
	// called while we hold it, or list↔discovery lock order can deadlock.
	name := ""
	if l.NameOf != nil {
		name = l.NameOf(from)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.e = slices.DeleteFunc(l.e, func(en Entry) bool { return en.Text == text })
	l.e = slices.Insert(l.e, 0, Entry{Text: text, From: from, Name: name, At: at})
	if len(l.e) > MaxEntries {
		clear(l.e[MaxEntries:]) // don't keep the evicted clip's text reachable
		l.e = l.e[:MaxEntries]
	}
}

// Entries returns a copy of the list, newest first; never nil.
func (l *List) Entries() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.e) == 0 {
		return []Entry{}
	}
	return slices.Clone(l.e)
}

// Clear empties the list (RECENT-12; this only affects this device).
func (l *List) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.e = nil
}
