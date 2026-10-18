package autostart

import (
	"path/filepath"
	"testing"
)

func TestTranslocated(t *testing.T) {
	if !Translocated("/private/var/folders/nh/x/T/AppTranslocation/FC6C/d/Kopi.app/Contents/MacOS/kopi") {
		t.Error("translocated path not detected")
	}
	if Translocated("/Users/u/Applications/Kopi.app/Contents/MacOS/kopi") {
		t.Error("normal install path reported as translocated")
	}
}

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
