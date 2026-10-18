package main

import (
	"errors"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

const createNoWindow = 0x08000000

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
