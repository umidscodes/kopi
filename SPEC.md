# Kopi — Spec (v1)

Copy text on one device, paste it on another. Kopi runs in the background on each of
your machines, finds the others on the local network, and keeps their clipboards in sync.

Personal project. Not a product: no installers, no code signing, no telemetry.

## Goals

- Text copied on any device appears in the clipboard of every other Kopi device on the LAN.
- Zero manual peer setup: devices discover each other automatically.
- Only devices that know the shared passphrase can read or write your clipboard.
- Lives in the system tray; otherwise invisible.

## Non-goals (v1)

- Images, files, rich text/HTML (plain text only).
- Sync outside the local network (Tailscale/remote peers are v2).
- Clipboard history.
- Linux support (nice to have, not required).

## Platforms

- Windows 11 (x64)
- macOS 27 (Apple Silicon)

## Glossary

- **Peer**: another device running Kopi with the same passphrase.
- **Clip**: one piece of copied text.
- **Sensitive clip**: text the source app marked as private (password managers do this).

## Acceptance criteria

### Feature: Sync

```gherkin
Scenario: [SYNC-1] Text copied on one device is available on another
  Given Kopi is running on device A and device B with the same passphrase
  And they are on the same network
  When I copy "hello" on device A
  Then within 1 second the clipboard on device B contains "hello"

Scenario: [SYNC-2] Sync works in both directions
  Given A and B are syncing
  When I copy "from B" on device B
  Then the clipboard on device A contains "from B"

Scenario: [SYNC-3] Sync reaches every peer
  Given Kopi is running on devices A, B and C
  When I copy "all of you" on device A
  Then the clipboards on B and C both contain "all of you"

Scenario: [SYNC-4] A received clip is never re-broadcast (no loops)
  Given A, B and C are syncing
  When I copy "once" on device A
  Then each peer receives "once" exactly one time
  And no device sends "once" back out after receiving it

Scenario: [SYNC-5] Copying the same text twice still syncs
  Given A and B are syncing
  And the last synced clip was "x"
  When I copy "y" on A and then "x" again on A
  Then B ends with "x"

Scenario: [SYNC-6] Latest copy wins
  Given A and B are syncing
  When A copies "first" and B copies "second" shortly after
  Then both devices end with "second"

Scenario: [SYNC-7] Unicode survives the trip
  When I copy "héllo 🌍 你好\r\nline2" on A
  Then B receives the same characters
  And line endings are converted to the receiving OS's convention
```

### Feature: Discovery

```gherkin
Scenario: [DISC-1] Peers find each other automatically
  Given Kopi is started on A and B on the same LAN
  Then within 10 seconds each lists the other as a peer in the tray menu

Scenario: [DISC-2] A peer that goes away is removed
  Given A and B are peers
  When Kopi on B quits
  Then within 30 seconds A no longer lists B

Scenario: [DISC-3] A peer that restarts is found again
  Given A and B are peers
  When B restarts Kopi
  Then A lists B again and sync resumes without user action
```

### Feature: Security

```gherkin
Scenario: [SEC-1] Wrong passphrase cannot sync
  Given A uses passphrase "alpha" and B uses "bravo"
  When I copy "secret" on A
  Then B's clipboard is unchanged
  And B's tray shows A as "passphrase mismatch" (not as a normal peer)

Scenario: [SEC-2] Traffic is encrypted
  When a clip is sent between peers
  Then the clip text does not appear in plain form in network traffic

Scenario: [SEC-3] Replayed messages are rejected
  Given an attacker captured an earlier encrypted message from A
  When the attacker re-sends it to B
  Then B ignores it

Scenario: [SEC-4] No passphrase means no sync
  Given Kopi starts with no passphrase configured
  Then it does not send or accept clips
  And the tray tells me to set a passphrase
```

### Feature: Sensitive content

```gherkin
Scenario: [SENS-1] Sensitive clips are skipped by default
  Given "Include sensitive content" is off (default)
  When I copy a password from a password manager on A
  Then nothing is sent to any peer

Scenario: [SENS-2] Sensitive clips can be opted in
  Given "Include sensitive content" is on
  When I copy a password from a password manager on A
  Then B receives it
```

Detection: Windows clipboard formats `ExcludeClipboardContentFromMonitorProcessing` and
`CanIncludeInClipboardHistory` = 0; macOS pasteboard types `org.nspasteboard.ConcealedType`
and `org.nspasteboard.TransientType`.

### Feature: Size limit

```gherkin
Scenario: [SIZE-1] Oversized clips are not sent
  When I copy more than 15 MB of text on A
  Then A does not send it
  And A's tray briefly shows "Clip too large (max 15 MB)"

Scenario: [SIZE-2] Oversized clips are rejected on receive
  Given a peer sends a message larger than 15 MB
  Then the receiver rejects it without reading it fully into memory

Scenario: [SIZE-3] Exactly at the limit
  When I copy exactly 15 MB of text on A
  Then B receives it
```

15 MB = 15 × 1024 × 1024 bytes of UTF-8 text.

### Feature: Tray

```gherkin
Scenario: [TRAY-1] Tray menu
  Then the tray menu shows:
    | Item                          |
    | Status (Syncing / Paused / …) |
    | Peers (list of device names)  |
    | Pause sync (toggle)           |
    | Include sensitive content     |
    | Set passphrase…               |
    | Quit                          |

Scenario: [TRAY-2] Pausing stops both directions
  Given sync is paused on A
  When I copy on A, or a peer sends to A
  Then nothing is sent from A and A's clipboard is not changed by peers
```

### Feature: Configuration

- Config file in the OS user config dir (`%APPDATA%\kopi\kopi.yml`, `~/Library/Application Support/kopi/kopi.yml`).
- Holds: device name (defaults to hostname), passphrase, include-sensitive flag, port.
- The passphrase is never logged and never committed to the repo.

## Technical constraints

- Go, single binary per OS. Build with `go build ./...`; test with `go test ./...`.
- Clipboard, tray and network access sit behind interfaces so core logic is testable with fakes.
- Discovery via mDNS/DNS-SD (service `_kopi._tcp`).
- Encryption key derived from the passphrase (memory-hard KDF), authenticated encryption per
  message, message IDs and timestamps for replay protection. Use vetted libraries
  (`golang.org/x/crypto`); no hand-rolled crypto.
- Clipboard watching: change notifications on Windows, change-count polling (~300 ms) on macOS.

## Known risks (verify manually on real hardware)

| Risk | Manual check |
|------|--------------|
| macOS Local Network permission denied → discovery silently fails | Deny the prompt, confirm tray shows a clear error |
| macOS 27 pasteboard privacy prompts for background reads | Run on the Mac, copy from several apps, note any prompts |
| Windows Firewall prompt on first listen | First run on Windows, confirm peers connect after allowing |
| Unsigned binary blocked by Gatekeeper | Document the one-time bypass in README |

## Definition of done (v1)

- All scenarios above pass: automated where possible (fakes + multi-peer integration tests),
  manual checklist for the "Known risks" table on a real Windows PC and Mac.
- CI runs `go vet` and `go test ./...` on Windows and macOS for every push.
- Tagged releases attach Windows and macOS binaries.
- README covers setup on each OS and starting at login.
