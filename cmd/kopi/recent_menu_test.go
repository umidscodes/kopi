package main

import (
	"strings"
	"testing"
	"time"

	"github.com/umidscodes/kopi/internal/recent"
)

var menuNow = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// RECENT-11: no entries means no slots; the menu then shows the disabled
// "No recent clips" item and a disabled Clear.
func TestRECENT11_EmptyMenuShowsNoRecentClips(t *testing.T) {
	if got := recentMenu(nil, menuNow, "self"); len(got) != 0 {
		t.Fatalf("slots for an empty list = %+v, want none", got)
	}
	if noRecentLabel != "No recent clips" {
		t.Fatalf("empty label = %q", noRecentLabel)
	}
}

// RECENT-10 (with RECENT-8 and RECENT-9): labels are "<preview> — <source> — <age>".
func TestRECENT10_MenuLabelsFromEntries(t *testing.T) {
	entries := []recent.Entry{
		{Text: "x", From: "self", At: menuNow.Add(-5 * time.Second)},
		{Text: " \n\t ", From: "peer-1", Name: "Mac", At: menuNow.Add(-119 * time.Second)},
		{Text: "from unnamed", From: "abcdef123456", Name: "", At: menuNow.Add(-3 * time.Hour)},
	}
	got := recentMenu(entries, menuNow, "self")
	want := []string{
		"x — this device — just now",
		"(blank) — Mac — 1 min",
		"from unnamed — abcdef — 3 h",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d slots, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Label != want[i] {
			t.Errorf("slot %d label = %q, want %q", i, got[i].Label, want[i])
		}
	}
}

func TestRECENT10_MenuShowsAtMostFiveSlots(t *testing.T) {
	var entries []recent.Entry
	for i := 0; i < 8; i++ {
		entries = append(entries, recent.Entry{Text: "t", From: "self", At: menuNow})
	}
	if got := recentMenu(entries, menuNow, "self"); len(got) != recentSlots {
		t.Fatalf("got %d slots, want %d", len(got), recentSlots)
	}
}

// RECENT-5: a click restores the full text of the entry drawn in that slot, taken
// from the drawn snapshot and not from whatever the list holds now.
func TestRECENT5_SlotMapsToDrawnEntry(t *testing.T) {
	long := strings.Repeat("long text ", 20)
	drawn := recentMenu([]recent.Entry{
		{Text: "y", From: "self", At: menuNow},
		{Text: long, From: "self", At: menuNow},
	}, menuNow, "self")
	// A clip arriving after the draw would shift a fresh read; the snapshot must not move.
	_ = recentMenu([]recent.Entry{{Text: "new", From: "self", At: menuNow}, {Text: "y", From: "self", At: menuNow}}, menuNow, "self")

	if text, ok := slotText(drawn, 1); !ok || text != long {
		t.Fatalf("slot 1 = %q, %v; want the full long text", text, ok)
	}
	if text, ok := slotText(drawn, 0); !ok || text != "y" {
		t.Fatalf("slot 0 = %q, %v; want y", text, ok)
	}
	for _, i := range []int{-1, 2, 4, 5} {
		if _, ok := slotText(drawn, i); ok {
			t.Errorf("slot %d should be unused", i)
		}
	}
}
