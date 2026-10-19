package recent

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// TestRECENT8_Preview covers SPEC.md [RECENT-8]: each line break ("\r\n" or "\n")
// and each tab becomes one space; leading/trailing whitespace is trimmed; the
// first 40 Unicode characters are shown, followed by "…" only if the trimmed text
// was longer than 40 characters. An empty-after-trim clip previews as "(blank)".
func TestRECENT8_Preview(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "hello", "hello"},
		{
			name: "crlf and surrounding spaces, truncated",
			in:   "  Dear team,\r\nthe release is ready for testing today.  ",
			want: "Dear team, the release is ready for test…",
		},
		{
			name: "exactly 40 characters, no ellipsis",
			in:   "0123456789012345678901234567890123456789",
			want: "0123456789012345678901234567890123456789",
		},
		{
			name: "unicode runes not bytes",
			in:   "héllo 🌍 你好",
			want: "héllo 🌍 你好",
		},
		{
			name: "40 multi-byte characters shown in full",
			in:   strings.Repeat("你", maxPreviewRunes),
			want: strings.Repeat("你", maxPreviewRunes),
		},
		{
			name: "41 emoji cut at 40 runes, valid UTF-8",
			in:   strings.Repeat("🌍", maxPreviewRunes+1),
			want: strings.Repeat("🌍", maxPreviewRunes) + "…",
		},
		{
			name: "41 ASCII characters, truncated",
			in:   strings.Repeat("0123456789", 4) + "0",
			want: strings.Repeat("0123456789", 4) + "…",
		},
		{"tab becomes a space", "a\tb", "a b"},
		{"all whitespace is blank", "  \n\t  ", "(blank)"},
		{"empty string is blank", "", "(blank)"},
		{"lf line break", "a\nb", "a b"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Preview(tc.in)
			if got != tc.want {
				t.Errorf("Preview(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("Preview(%q) = %q, which is not valid UTF-8", tc.in, got)
			}
		})
	}
}

// TestRECENT9_SourceLabel covers SPEC.md [RECENT-9] and ticket #7 decision 4:
// "this device" when the clip came from this device; otherwise the stored peer
// name if non-empty; otherwise the first 6 characters of the peer ID (the whole
// ID if shorter). No name lookup happens here.
func TestRECENT9_SourceLabel(t *testing.T) {
	tests := []struct {
		name   string
		from   string
		stored string
		self   string
		want   string
	}{
		{"self device", "aaaa-1", "", "aaaa-1", "this device"},
		{"self device, stored name ignored", "aaaa-1", "Mac", "aaaa-1", "this device"},
		{"known peer name", "bbbb-2", "Mac", "aaaa-1", "Mac"},
		{"unknown peer name falls back to ID prefix", "bbbb22cc-2", "", "aaaa-1", "bbbb22"},
		{"short ID shown whole", "abc", "", "aaaa-1", "abc"},
		{
			name:   "multi-byte ID prefix uses runes",
			from:   "日本語のID名前",
			stored: "",
			self:   "aaaa-1",
			want:   "日本語のID",
		},
		{
			name:   "empty selfID never labels this device",
			from:   "",
			stored: "",
			self:   "",
			want:   "",
		},
		{
			name:   "empty selfID falls back to the ID",
			from:   "bbbb-2",
			stored: "",
			self:   "",
			want:   "bbbb-2",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Source(tc.from, tc.stored, tc.self); got != tc.want {
				t.Errorf("Source(%q, %q, %q) = %q, want %q",
					tc.from, tc.stored, tc.self, got, tc.want)
			}
		})
	}
}

// TestRECENT10_AgeLabel covers SPEC.md [RECENT-10]: under 60 s "just now",
// 1-59 min "N min", 1-23 h "N h", 24 h or more "N d", N rounded down.
func TestRECENT10_AgeLabel(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "just now"},
		{59 * time.Second, "just now"},
		{60 * time.Second, "1 min"},
		{119 * time.Second, "1 min"},
		{59*time.Minute + 59*time.Second, "59 min"},
		{60 * time.Minute, "1 h"},
		{23*time.Hour + 59*time.Minute, "23 h"},
		{24 * time.Hour, "1 d"},
		{49 * time.Hour, "2 d"},
	}
	for _, tc := range tests {
		t.Run(tc.want+"@"+tc.d.String(), func(t *testing.T) {
			if got := Age(tc.d); got != tc.want {
				t.Errorf("Age(%v) = %q, want %q", tc.d, got, tc.want)
			}
		})
	}
}
