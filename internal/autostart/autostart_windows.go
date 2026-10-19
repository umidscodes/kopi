package autostart

import (
	"errors"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// runKey is a variable so tests can point it at a throwaway key.
var runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

const valueName = "Kopi"

// Enabled reports whether Kopi starts at login *from this executable*. An entry
// pointing at another copy (e.g. an old install location) counts as not enabled.
func Enabled(exe string) (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer k.Close()
	v, _, err := k.GetStringValue(valueName)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return strings.EqualFold(v, quote(exe)), nil
}

// Enable makes Kopi start at login from exe.
func Enable(exe string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(valueName, quote(exe))
}

// Disable removes the login entry. Removing a missing entry is not an error.
func Disable() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.DeleteValue(valueName); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// quote wraps the path in quotes so paths with spaces work.
func quote(exe string) string { return `"` + exe + `"` }
