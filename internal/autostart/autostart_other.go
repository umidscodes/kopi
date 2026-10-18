//go:build !windows && !darwin

package autostart

import "errors"

var errUnsupported = errors.New("start at login isn't supported on this platform")

func Enabled(string) (bool, error) { return false, nil }
func Enable(string) error          { return errUnsupported }
func Disable() error               { return errUnsupported }
