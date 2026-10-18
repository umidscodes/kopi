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
| S5 | macOS: quit Kopi, unzip, replace `~/Applications/Kopi.app`, open it | Gatekeeper blocks it; Done → Privacy & Security → Open Anyway (as in README) works; menu-bar icon appears |
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
| T06 | SENS-1 | Include sensitive **off** | Copy a password from your password manager on PC | Mac clipboard unchanged; PC tray flashes "Skipped a sensitive clip" |
| T07 | SENS-2 | Include sensitive **on** | Repeat T06 | Password arrives on Mac. Turn the option off again afterwards |
| T08 | SIZE-1 | Synced pair | Copy > 15 MB of text (e.g. a large log file) on PC | Not sent; tray flashes "Clip too large (max 15 MB)" |
| T09 | TRAY-2 | Synced pair | Tick Pause sync on PC; copy on PC; copy on Mac | Nothing crosses either way; status "Paused". Untick → syncing resumes |
| T10 | SEC-1 | Synced pair | Set a *different* passphrase on Mac; copy on PC | Mac clipboard unchanged; both list the other as "passphrase mismatch". Restore the passphrase → syncing resumes |
| T11 | SEC-4 | Remove the passphrase line from one device's `kopi.yml` | Wait 5 s; copy on it | Status "Set a passphrase to start syncing"; nothing sent |
| T12 | DISC-1 | Both running | Quit and reopen Kopi on Mac | PC lists Mac again within ~10 s |
| T13 | DISC-2 | Both running | Quit Kopi on Mac | PC drops Mac within 30 s |
| T14 | DISC-3 | After T13 | Reopen Kopi on Mac; copy on PC | Mac listed again; clip arrives, no user action |
| T15 | DISC-4 | Tailscale on both | Turn Wi-Fi off on the Mac and use a phone hotspot (different network); copy on PC | Clip still arrives (via tailnet) |
| T16 | DISC-7 | Only one device running | Wait 35 s; open the tray | Status suggests checking firewall / Tailscale |
| T17 | — | Synced pair | Put the Mac to sleep for 2 min, wake it; copy on PC | Clip arrives within ~20 s of waking |
| T18 | — | Start at login **on** (both) | Log out and back in (or reboot) | Kopi is running; tray shows it; syncing works |
| T19 | — | After T18 | Untick Start at login; log out/in | Kopi does not start |
| T20 | — | Passphrase dialog | Click Set passphrase… twice quickly (Windows) | Exactly one dialog, in front of other windows |
| T21 | Risk | Fresh Mac install | Deny Local Network on first launch | Mac finds no LAN peers but still finds PC via tailnet; no crash |
| T22 | — | Synced pair | Check both `kopi.log` files after the run | No clip text or passphrase anywhere in the logs |

## Results — vX.Y.Z

| ID | Windows → Mac | Mac → Windows | Notes / issue |
|----|---------------|---------------|---------------|
| T01–T22 | | | |

**Sign-off:** ______ (name) · date ______ · build `vX.Y.Z` · decision: ship / don't ship
