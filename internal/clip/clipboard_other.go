//go:build !windows && !darwin

package clip

import "errors"

// LineEnding is the convention for text written to this OS's clipboard.
const LineEnding = "\n"

// New is not implemented on this platform yet (SPEC.md: Windows and macOS only).
func New() (Clipboard, error) {
	return nil, errors.New("kopi: no clipboard support on this platform")
}
