// Package recent formats "Recent clips" tray entries: preview text, source
// label and age label. See SPEC.md scenarios RECENT-8, RECENT-9, RECENT-10.
package recent

import (
	"strconv"
	"strings"
	"time"
)

// maxPreviewRunes is the number of characters a preview shows before an
// ellipsis (RECENT-8).
const maxPreviewRunes = 40

// blankPreview is the preview of a clip that is empty after trimming
// (RECENT-8, ticket #7 decision 1).
const blankPreview = "(blank)"

// Preview turns clip text into the one-line preview shown in the tray menu:
// each line break ("\r\n" or "\n") and each tab becomes one space, leading and
// trailing whitespace is trimmed, and the first 40 Unicode characters are
// shown, followed by "…" only if the text was longer (RECENT-8).
func Preview(text string) string {
	s := strings.ReplaceAll(text, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.TrimSpace(s)
	if s == "" {
		return blankPreview
	}
	r := []rune(s)
	if len(r) <= maxPreviewRunes {
		return s
	}
	return string(r[:maxPreviewRunes]) + "…"
}

// sourceSelfIDChars is how many leading characters of a peer ID identify it
// when no name was stored with the entry (RECENT-9).
const sourceSelfIDChars = 6

// Source names the device a clip came from (RECENT-9): "this device" when the
// clip was recorded on this device (an empty selfID identifies nothing, so an
// empty selfID never matches); otherwise the name stored with the entry when
// the clip was recorded (ticket #7 decision 4 — no name lookup happens here);
// failing that, the first 6 characters of the peer ID.
func Source(from, name, selfID string) string {
	if selfID != "" && from == selfID {
		return "this device"
	}
	if name != "" {
		return name
	}
	r := []rune(from)
	if len(r) <= sourceSelfIDChars {
		return from
	}
	return string(r[:sourceSelfIDChars])
}

// Age formats how long ago this device sent or applied a clip (RECENT-10):
// under 60 s "just now", 1-59 minutes "N min", 1-23 hours "N h", 24 hours or
// more "N d", with N rounded down.
func Age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + " min"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + " h"
	default:
		return strconv.Itoa(int(d.Hours()/24)) + " d"
	}
}
