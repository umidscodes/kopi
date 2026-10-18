package main

import (
	"errors"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

const (
	createNoWindow = 0x08000000
	asfwAny        = ^uintptr(0) // ASFW_ANY: any process may take the foreground once
)

var procAllowSetForegroundWindow = windows.NewLazySystemDLL("user32.dll").NewProc("AllowSetForegroundWindow")

// dialogPassphrase shows the standard Windows credential prompt (masked input).
// The passphrase comes back over a private stdout pipe, never through a file.
func dialogPassphrase(prompt string) (string, error) {
	script := `$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.Encoding]::UTF8
try { $c = Get-Credential -UserName 'Kopi' -Message '` + strings.ReplaceAll(prompt, "'", "''") + `' } catch { exit 2 }
if ($null -eq $c) { exit 2 }
[Console]::Out.Write($c.GetNetworkCredential().Password)`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	// Windows stops background programs from putting windows in front of the user's
	// work, so the dialog would open hidden behind other windows. The tray click made
	// us eligible to take the foreground; pass that right on to the dialog process.
	procAllowSetForegroundWindow.Call(asfwAny)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", errCancelled
		}
		return "", err
	}
	return string(out), nil
}

func showMessage(msg string) {
	text, _ := windows.UTF16PtrFromString(msg)
	title, _ := windows.UTF16PtrFromString("Kopi")
	windows.MessageBox(0, text, title, windows.MB_OK|windows.MB_ICONINFORMATION)
}
