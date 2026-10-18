# Kopi

Copy text on one machine, paste it on another. Kopi sits in the tray on each of your
Windows and macOS devices, finds the others automatically (LAN or Tailscale), and keeps
their clipboards in sync — plain text only, encrypted with a shared passphrase you set.
It is a personal project, not a product: no installers, no code signing, no telemetry.

## How it works

- A small tray / menu-bar app watches your clipboard and sends each clip to your other
  devices; received text lands in the local clipboard with line endings converted.
- Devices find each other through mDNS on the LAN, and — when Tailscale is installed —
  through the tailnet device list, confirmed with a quick hello before being listed.
- Traffic is encrypted end to end: your passphrase is stretched into a key with
  Argon2id, and each message is sealed with XChaCha20-Poly1305. A wrong passphrase
  means nothing decrypts; replayed messages are rejected.
- Sensitive copies (password managers and the like) are **not** synced unless you turn
  on "Include sensitive content". Clips over 15 MB are not sent.
- Nothing leaves your devices: no server, no accounts, no cloud.

## Install

Grab the latest release from the
[Releases page](https://github.com/umidscodes/kopi/releases):

- `kopi-<version>-windows-amd64.zip`
- `kopi-<version>-macos-arm64.zip`
- `SHA256SUMS`

Verify the checksum before unpacking:

```powershell
# Windows
Get-FileHash -Algorithm SHA256 kopi-v0.1.0-windows-amd64.zip
```

```sh
# macOS
shasum -a 256 kopi-v0.1.0-macos-arm64.zip
```

The hash must match the corresponding line in `SHA256SUMS`.

### Windows

Unzip `kopi.exe` to `%LOCALAPPDATA%\Programs\Kopi\` and run it. A tray icon appears.

The build is unsigned, so Windows may warn: on "Windows protected your PC" click
**More info → Run anyway**. When the firewall prompt appears, allow Kopi for
**private networks only**. Antivirus apps with their own firewall (e.g. AVG) may block
inbound traffic silently and need an explicit allow rule for `kopi.exe` (and, for
sandboxing antivirus, an exception for the `Kopi` folder). Re-add both if you later
move the install path.

### macOS

Unzip and move `Kopi.app` to `~/Applications`, then open it. The icon appears in the
menu bar.

The app is built on GitHub's runners and is not notarized, so Gatekeeper blocks the
first open ("could not verify … free of malware"). Click **Done** (not Move to Trash),
then either:

- System Settings → Privacy & Security → scroll to **Security** → next to
  *"Kopi" was blocked…* click **Open Anyway**, confirm with your password, then **Open**
  (the button shows for about an hour after the blocked attempt), or
- clear the quarantine flag yourself:

```sh
xattr -dr com.apple.quarantine ~/Applications/Kopi.app
```

(Right-click → Open no longer bypasses Gatekeeper since macOS 15.)

Prefer the `xattr` command: after "Open Anyway" alone, macOS keeps running the app
from a hidden temporary copy ("App Translocation"), and Kopi's tray will say so.

macOS then asks whether Kopi may find devices on your local network — click **Allow**.
If you missed the prompt: System Settings → Privacy & Security → Local Network →
turn **Kopi** on.

## First run

1. Click the tray / menu-bar icon → **Set passphrase…** and enter a passphrase
   (at least 8 characters, typed twice). Set the **same** passphrase on every device;
   that is the only "pairing" there is.
2. The menu shows what's going on:
   - a status line (`Kopi — Syncing`, `Paused`, `Set a passphrase to start syncing`,
     or a hint if nothing was found),
   - **Devices**: your peers, each marked `connected`, `found`,
     `passphrase mismatch` or `unreachable`,
   - **Pause sync** — stop sending and receiving,
   - **Send passwords copied on this device** — off by default; it's the *copying*
     device's setting that decides,
   - **Set passphrase…**, **Start at login** — off by default,
   - **Quit Kopi**.

Command-line alternative to the dialog (a running app picks it up within seconds):

```sh
kopi passphrase
```

## Tailscale (optional, recommended)

If both devices are on the same Tailscale network (tailnet), Kopi finds them even when
LAN discovery fails — it queries the `tailscale` CLI for online devices and says hello
to each on port 47800. This also works when the two machines are on different
networks. Nothing to configure in Kopi; install Tailscale and sign in on both devices.

## Troubleshooting

| Symptom | What to check |
|---|---|
| No devices found | Same LAN? Firewall allowing Kopi on private networks? Tailscale installed on both? After 30 s alone, the status line says what to check. |
| "passphrase mismatch" | The two devices have different passphrases; set them the same. |
| "unreachable" | A device is announced but its port is blocked. From the other machine: `go run ./tools/kopiping <ip>:47800` — OK with a name means it's reachable. |
| Passphrase dialog doesn't appear | Only one dialog at a time; check other windows. Or run `kopi passphrase` in a terminal. |
| A clip didn't arrive | Sync paused? Was it marked sensitive (skipped unless the *copying* device allows it)? Over 15 MB? Is the other device's Kopi running? Right after waking from sleep Kopi retries for ~30 s. |
| Settings don't stick (Windows) | Antivirus sandboxing (e.g. AVG CyberCapture / Sandbox) lets Kopi run but throws away what it saves. Exclude `kopi.exe` from the sandbox and add it to the exceptions. |
| Tray says "running from a temporary copy" (macOS) | Run `xattr -dr com.apple.quarantine ~/Applications/Kopi.app` and reopen Kopi. |

Logs: `%APPDATA%\kopi\kopi.log` (Windows), `~/Library/Application Support/kopi/kopi.log`
(macOS). Settings live next to the log, in `kopi.yml`. Logs never contain clip text or
the passphrase.

## Build from source

Go 1.27 or newer (see `go.mod`); on macOS, Xcode command-line tools (the clipboard
code uses cgo).

```powershell
# Windows: bin\kopi.exe, no console window
powershell -ExecutionPolicy Bypass -File scripts\build-windows.ps1
```

```sh
# macOS: bin/Kopi.app; add --install to copy it to ~/Applications
scripts/build-mac-app.sh v0.1.0 --install
```

```sh
go build ./...
go vet ./...
go test ./...
```

Tests that touch the real clipboard or live mDNS are opt-in:

```sh
KOPI_CLIPBOARD_TEST=1 go test ./internal/clip/ -v
KOPI_MDNS_TEST=1 go test ./internal/discovery/ -run MDNS -v
```

## Releasing

Push a version tag and the Release workflow builds both binaries, publishes
`SHA256SUMS` and creates the GitHub Release:

```sh
git tag v0.1.0 && git push origin v0.1.0
```

A tag with a hyphen (e.g. `v0.1.0-rc1`) is published as a pre-release.

## Project docs

- [SPEC.md](SPEC.md) — required behavior, as testable scenarios.
- [PLAN.md](PLAN.md) — architecture, design decisions, milestones.
- [AGENTS.md](AGENTS.md) — instructions for coding agents working here.
- [TESTPLAN.md](TESTPLAN.md) — manual checklist to run before tagging a release.
