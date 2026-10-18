package discovery

import (
	"os/exec"
	"syscall"
)

// hideWindow stops a console window from flashing up when the tray app runs a CLI.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
