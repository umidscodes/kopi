package main

import (
	"time"

	"github.com/umidscodes/kopi/internal/recent"
)

const (
	// recentSlots is how many entries the submenu shows (RECENT-1).
	recentSlots = 5
	// noRecentLabel is the disabled item shown when the list is empty (RECENT-11).
	noRecentLabel = "No recent clips"
	// recentRefresh is the longest the age labels may go without a redraw (RECENT-10).
	recentRefresh = 60 * time.Second
)

// recentSlot is what one submenu slot shows and what a click on it restores.
type recentSlot struct {
	Label string // "<preview> — <source> — <age>"
	Text  string // full clip text, restored on click
}

// recentMenu builds the submenu slots from a snapshot of the list, newest first. It is
// pure (no systray, no hub lookups) so it can be tested without a GUI; the source name
// is the one stored with each entry. An empty result means "No recent clips".
func recentMenu(entries []recent.Entry, now time.Time, selfID string) []recentSlot {
	if len(entries) > recentSlots {
		entries = entries[:recentSlots]
	}
	slots := make([]recentSlot, len(entries))
	for i, e := range entries {
		slots[i] = recentSlot{
			Label: recent.Preview(e.Text) + " — " + recent.Source(e.From, e.Name, selfID) +
				" — " + recent.Age(now.Sub(e.At)),
			Text: e.Text,
		}
	}
	return slots
}

// slotText returns the text of slot i in the drawn snapshot, and false if the slot is
// out of range or unused.
func slotText(slots []recentSlot, i int) (string, bool) {
	if i < 0 || i >= len(slots) {
		return "", false
	}
	return slots[i].Text, true
}

// recentState says what the submenu items look like for a drawn snapshot.
type recentState struct {
	SlotVisible  [recentSlots]bool
	ShowNone     bool // the disabled "No recent clips" item
	ClearEnabled bool
}

// recentMenuState decides which slots show, whether "No recent clips" shows and whether
// Clear is enabled (RECENT-11). Pure, so it is testable without a GUI.
func recentMenuState(slots []recentSlot) recentState {
	var st recentState
	for i := range st.SlotVisible {
		st.SlotVisible[i] = i < len(slots)
	}
	st.ShowNone = len(slots) == 0
	st.ClearEnabled = len(slots) > 0
	return st
}
