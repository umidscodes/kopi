//go:build !windows

package discovery

import "os/exec"

func hideWindow(*exec.Cmd) {}
