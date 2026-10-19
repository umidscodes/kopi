# Kopi — Release Test Plan

Manual checks run against the **released build** (downloaded from GitHub Releases and
installed in the normal locations), on the real Windows PC and Mac, before a version is
tagged final. Automated tests cover the logic; this plan covers what only real
hardware, real apps and real users can show.

How to use it: copy the "Results" table for each release, fill in Pass / Fail / Blocked
with a note, and file a GitHub issue for every Fail (title starts with the case ID).

## Setup (once per release)

| # | Step | Expected |
|---|------|----------|
| S1 | Download both zips and `SHA256SUMS` from the release page | Files present |
| S2 | Verify checksums (`Get-FileHash -Algorithm SHA256 <zip>` / `shasum -a 256 <zip>`) | Match `SHA256SUMS` |
| S3 | Windows: quit any running Kopi, unzip `kopi.exe` to `%LOCALAPPDATA%\Programs\Kopi\`, run it | SmartScreen warning → More info → Run anyway; tray icon appears |
| S4 | Windows: allow `…\Programs\Kopi\kopi.exe` in the firewall (Windows / AVG), private networks | Rule exists |
| S5 | macOS: quit Kopi, unzip, replace `~/Applications/Kopi.app`, then in Terminal `xattr -dr com.apple.quarantine ~/Applications/Kopi.app`, open it | Opens; menu-bar icon appears; status does **not** mention "temporary copy" |
| S5b | Windows: check AVG isn't sandboxing Kopi: tick Pause sync, quit, reopen | Pause sync is still unticked? Fine (not saved by design). Then toggle "Start at login" on, quit, reopen: it must still be ticked. If not, AVG is throwing away Kopi's writes — add `kopi.exe` to AVG's exceptions **and** exclude it from the Sandbox / CyberCapture |
| S6 | macOS: allow Local Network when asked | Kopi on in Privacy & Security → Local Network |
| S7 | Both: tray → Set passphrase…, same passphrase on both | Status "Kopi — Syncing"; each lists the other as *connected* after the first clip |

## Cases

| ID | Spec | Preconditions | Steps | Expected |
|----|------|---------------|-------|----------|
| T01 | SYNC-1 | Synced pair | Copy a sentence on PC, paste on Mac | Same text arrives within ~1 s |
| T02 | SYNC-2 | Synced pair | Copy on Mac, paste on PC | Same text arrives within ~1 s |
| T03 | SYNC-7 | Synced pair | Copy `héllo 🌍 你好` plus a second line, paste on the other side | Characters identical; line break preserved |
| T04 | SYNC-5 | Synced pair | Copy "x" on PC, then "y", then "x" again | Mac ends with "x" |
| T05 | SYNC-4 | Synced pair | Copy once on PC; watch both logs for 10 s | One "sent" on PC, one "applied" on Mac, no echo back |
| T06 | SENS-1 | On the **PC** (the copying device), "Send passwords copied on this device" **unticked** — check the tick mark first | Copy a password from your password manager on PC; open the PC tray within 3 s | Mac clipboard unchanged; PC status shows "Skipped a sensitive clip". Note which password manager you used: not every app marks copies as private |
| T07 | SENS-2 | On the **PC**, the option **ticked** | Repeat T06 | Password arrives on Mac. Untick it again afterwards |
| T08 | SIZE-1 | Synced pair | On PC, in PowerShell: `Set-Clipboard -Value ('x' * 15728641)` (15 MiB + 1 byte) | Not sent (Mac clipboard unchanged); PC status shows "Clip too large (max 15 MB)" |
| T08b | SIZE-3 | Synced pair | On PC: `Set-Clipboard -Value ('x' * 15728640)` (exactly 15 MiB) | Arrives on Mac. Don't paste it into a browser — 15 MB in one text field can hang the browser itself |
| T09 | TRAY-2 | Synced pair | Tick Pause sync on PC; copy on PC; copy on Mac | Nothing crosses either way; status "Paused". Untick → syncing resumes |
| T10 | SEC-1 | Synced pair | Set a *different* passphrase on Mac; copy on PC | Mac clipboard unchanged; both list the other as "passphrase mismatch". Restore the passphrase → syncing resumes |
| T11 | SEC-4 | Remove the passphrase line from one device's `kopi.yml` | Wait 5 s; copy on it | Status "Set a passphrase to start syncing"; nothing sent |
| T12 | DISC-1 | Both running | Quit and reopen Kopi on Mac | PC lists Mac again within ~10 s |
| T13 | DISC-2 | Both running | Quit Kopi on Mac | PC drops Mac within 30 s |
| T14 | DISC-3 | After T13 | Reopen Kopi on Mac; copy on PC | Mac listed again; clip arrives, no user action |
| T15 | DISC-4 | Tailscale on both | Turn Wi-Fi off on the Mac and use a phone hotspot (different network); copy on PC | Clip still arrives (via tailnet) |
| T16 | DISC-7 | Only one device running | Wait 35 s; open the tray | Status suggests checking firewall / Tailscale |
| T17 | — | Synced pair | Close the Mac's lid for 2 min; open it and **immediately** copy on PC | Clip arrives within ~30 s of waking, without copying again |
| T18 | — | Start at login **on** (both) | Log out and back in (or reboot) | Kopi is running; tray shows it; syncing works |
| T19 | — | After T18 | Untick Start at login; log out/in | Kopi does not start |
| T20 | — | Passphrase dialog | Click Set passphrase… twice quickly (Windows) | Exactly one dialog, in front of other windows |
| T21 | Risk | Fresh Mac install | Deny Local Network on first launch | Mac finds no LAN peers but still finds PC via tailnet; no crash |
| T22 | — | Synced pair | Check both `kopi.log` files after the run | No clip text or passphrase anywhere in the logs |

## Results — v0.1.0 (tested on v0.1.0-rc3, 2026-10-18)

Windows 11 PC (AVG antivirus) and MacBook Pro (macOS 27.2, Apple Silicon), Tailscale on both.

| ID | Result | Notes |
|----|--------|-------|
| S1–S7 | Pass | Mac: rc download unzipped as `Kopi-2.app` in Downloads and ran translocated until moved + `xattr`; rc3's tray warning caught it |
| S5b | Pass | Windows: settings and Start at login persist (after an AVG exception — AVG Auto-Sandbox had sandboxed and terminated `kopi.exe` once) |
| T01–T05 | Pass | |
| T06 | Pass | Google Password Manager (Chrome) copies are marked private and were not sent. Failed on rc1: the PC still had the option on from earlier exploring, and the label didn't say it's per copying device (renamed) |
| T07 | Pass | |
| T08 | Pass | 15 728 641-byte clip never arrived (verified from the Mac log). Failed on rc1 only because the test data was exactly 15 MiB |
| T08b | Pass | Exactly 15 728 640 bytes arrived |
| T09–T16 | Pass | |
| T17 | Pass | Failed on rc1/rc2: a clip copied during wake was dropped; rc3 retries for ~30 s |
| T18 | Pass | Windows auto-started after a reboot; macOS via LaunchAgent by bundle ID |
| T19–T22 | Pass | |

**Sign-off:** owner (QA) · 2026-10-18 · build `v0.1.0-rc3` → released as `v0.1.0` · decision: **ship**

## Results template — vX.Y.Z

| ID | Windows → Mac | Mac → Windows | Notes / issue |
|----|---------------|---------------|---------------|
| T01–T22 | | | |

**Sign-off:** ______ (name) · date ______ · build `vX.Y.Z` · decision: ship / don't ship
