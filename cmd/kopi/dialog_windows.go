package main

import (
	"errors"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

const createNoWindow = 0x08000000

// passphraseForm is a small WinForms dialog with one masked field. It is TopMost:
// Windows won't let a background program take the foreground (and after a tray click
// Kopi often isn't foreground), so a normal dialog opens hidden behind other windows.
// A TopMost window is drawn above them regardless of focus. {{PROMPT}} is replaced
// with the prompt, already escaped for a single-quoted PowerShell string.
const passphraseForm = `$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms, System.Drawing
[System.Windows.Forms.Application]::EnableVisualStyles()
$f = New-Object System.Windows.Forms.Form
$f.Text = 'Kopi'
$f.TopMost = $true
$f.StartPosition = 'CenterScreen'
$f.FormBorderStyle = 'FixedDialog'
$f.MaximizeBox = $false
$f.MinimizeBox = $false
$f.ShowInTaskbar = $true
$f.ClientSize = New-Object System.Drawing.Size(380, 128)
$l = New-Object System.Windows.Forms.Label
$l.Text = '{{PROMPT}}'
$l.SetBounds(14, 14, 352, 20)
$t = New-Object System.Windows.Forms.TextBox
$t.UseSystemPasswordChar = $true
$t.SetBounds(14, 42, 352, 24)
$ok = New-Object System.Windows.Forms.Button
$ok.Text = 'OK'
$ok.DialogResult = [System.Windows.Forms.DialogResult]::OK
$ok.SetBounds(210, 84, 75, 28)
$cancel = New-Object System.Windows.Forms.Button
$cancel.Text = 'Cancel'
$cancel.DialogResult = [System.Windows.Forms.DialogResult]::Cancel
$cancel.SetBounds(291, 84, 75, 28)
$f.AcceptButton = $ok
$f.CancelButton = $cancel
$f.Controls.AddRange(@($l, $t, $ok, $cancel))
$f.Add_Shown({ $f.Activate(); $t.Focus() })
if ($f.ShowDialog() -ne [System.Windows.Forms.DialogResult]::OK) { exit 2 }
[Console]::OutputEncoding = [Text.Encoding]::UTF8
[Console]::Out.Write($t.Text)`

// dialogPassphrase shows Kopi's masked passphrase window. The passphrase comes back
// over a private stdout pipe, never through a file.
func dialogPassphrase(prompt string) (string, error) {
	script := strings.Replace(passphraseForm, "{{PROMPT}}", strings.ReplaceAll(prompt, "'", "''"), 1)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-STA", "-Command", script)
	// No console window, but don't set HideWindow: that would also hide the form.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
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
	// MB_TOPMOST | MB_SETFOREGROUND: same reason as the passphrase form.
	windows.MessageBox(0, text, title, windows.MB_OK|windows.MB_ICONINFORMATION|windows.MB_TOPMOST|windows.MB_SETFOREGROUND)
}
