package autostart

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
)

// agentDir is a variable so tests can use a temp directory.
var agentDir = filepath.Join(os.Getenv("HOME"), "Library", "LaunchAgents")

func agentPath() string { return filepath.Join(agentDir, Label+".plist") }

// Enabled reports whether a LaunchAgent exists that starts this Kopi (for an app
// bundle: any installed Kopi.app, since the agent opens it by bundle ID).
func Enabled(exe string) (bool, error) {
	b, err := os.ReadFile(agentPath())
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return bytes.Equal(b, plist(exe)), nil
}

// Enable writes the LaunchAgent. It takes effect at the next login.
func Enable(exe string) error {
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(agentPath(), plist(exe), 0o644)
}

// Disable removes the LaunchAgent. Removing a missing one is not an error.
func Disable() error {
	err := os.Remove(agentPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// plist builds the LaunchAgent. Inside an app bundle it runs `open -b <bundle ID>`:
// macOS launches the app as itself (its Local Network permission stays attached),
// and finds it wherever it's installed. Using the path would break when the app
// runs from App Translocation's temporary copy (quarantined downloads), a path that
// is gone after a reboot. A bare binary (development build) is run directly.
func plist(exe string) []byte {
	args := []string{exe}
	if bundlePath(exe) != "" {
		args = []string{"/usr/bin/open", "-b", Label}
	}
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>` + Label + `</string>
	<key>ProgramArguments</key>
	<array>
`)
	for _, a := range args {
		b.WriteString("\t\t<string>" + xmlEscape(a) + "</string>\n")
	}
	b.WriteString(`	</array>
	<key>RunAtLoad</key><true/>
</dict>
</plist>
`)
	return b.Bytes()
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
