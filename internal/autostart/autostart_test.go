package autostart

import (
	"path/filepath"
	"testing"
)

func TestBundlePath(t *testing.T) {
	// FromSlash keeps the test valid on Windows, where filepath uses backslashes.
	for exe, want := range map[string]string{
		"/Users/u/Applications/Kopi.app/Contents/MacOS/kopi": "/Users/u/Applications/Kopi.app",
		"/Users/u/kopi/bin/kopi":                             "",
		"/Users/u/Weird/Contents/MacOS/kopi":                 "", // not inside a .app
	} {
		if want != "" {
			want = filepath.FromSlash(want)
		}
		if got := bundlePath(filepath.FromSlash(exe)); got != want {
			t.Errorf("bundlePath(%q) = %q, want %q", exe, got, want)
		}
	}
}
