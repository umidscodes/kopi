package main

import (
	"errors"
	"os/exec"
	"strings"
)

// dialogPassphrase shows a native masked input dialog.
func dialogPassphrase(prompt string) (string, error) {
	out, err := exec.Command("osascript",
		"-e", `set r to display dialog "`+escapeAS(prompt)+`" default answer "" with hidden answer with title "Kopi" buttons {"Cancel", "OK"} default button "OK"`,
		"-e", `text returned of r`,
	).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", errCancelled // osascript exits non-zero when Cancel is clicked
		}
		return "", err
	}
	return strings.TrimSuffix(string(out), "\n"), nil
}

func showMessage(msg string) {
	exec.Command("osascript", "-e",
		`display dialog "`+escapeAS(msg)+`" with title "Kopi" buttons {"OK"} default button "OK"`).Run()
}

// escapeAS escapes a string for an AppleScript string literal.
func escapeAS(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}
