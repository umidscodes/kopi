// Package autostart turns "start Kopi when I log in" on and off.
//
// Windows: a value named Kopi under HKCU\Software\Microsoft\Windows\CurrentVersion\Run.
// macOS: a LaunchAgent in ~/Library/LaunchAgents that opens Kopi.app at login.
// Both are per-user, need no admin rights, and are removed again by Disable.
package autostart

import (
	"os"
	"path/filepath"
	"strings"
)

// Label identifies Kopi's login entry on every OS.
const Label = "io.github.umidscodes.kopi"

// Executable is the running program, with symlinks resolved.
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// bundlePath returns the .app bundle containing exe, or "" if exe isn't in one.
// ".../Kopi.app/Contents/MacOS/kopi" → ".../Kopi.app"
func bundlePath(exe string) string {
	dir := filepath.Dir(exe) // .../Contents/MacOS
	if filepath.Base(dir) != "MacOS" {
		return ""
	}
	contents := filepath.Dir(dir)
	if filepath.Base(contents) != "Contents" {
		return ""
	}
	app := filepath.Dir(contents)
	if !strings.HasSuffix(app, ".app") {
		return ""
	}
	return app
}
