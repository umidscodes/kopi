# Kopi — Spec (v1)

Copy text on one device, paste it on another. Kopi runs in the background on each of
your machines, finds the others on the local network, and keeps their clipboards in sync.

Personal project. Not a product: no installers, no code signing, no telemetry.

## Goals

- Text copied on any device appears in the clipboard of every other Kopi device on the LAN,
  or on the same Tailscale network (tailnet).
- Zero manual peer setup: devices discover each other automatically, through mDNS on the
  LAN and, when Tailscale is installed, through the tailnet's device list.
- Only devices that know the shared passphrase can read or write your clipboard.
- Lives in the system tray; otherwise invisible.

## Non-goals (v1)

- Images, files, rich text/HTML (plain text only).
- Sync over the open internet, or with devices that are neither on the LAN nor on your
  tailnet. (Tailnet devices were added in v1 after mDNS proved unreliable on a real PC:
  browsers and launchers that also listen on UDP 5353 can starve Kopi of mDNS traffic.)
- Clipboard history that is saved to disk, survives a restart or holds more than a few
  entries. (v0.2 adds only a memory-only list of the last 5 synced clips; see
  Feature: Recent clips.)
- Linux support (nice to have, not required).

## Platforms

- Windows 11 (x64)
- macOS 27 (Apple Silicon)

## Glossary

- **Peer**: another device running Kopi with the same passphrase.
- **Clip**: one piece of copied text.
- **Sensitive clip**: text the source app marked as private (password managers do this).
- **Recent clips**: the tray submenu listing the last clips this device synced (v0.2).

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

Scenario: [DISC-4] Tailnet peers are found even when LAN discovery fails
  Given A and B are on the same tailnet and both run Kopi
  And mDNS traffic between them is blocked
  Then within 20 seconds each lists the other as a peer
  And clips sync between them

Scenario: [DISC-5] A device found both ways is listed once
  Given B is discovered through mDNS and through the tailnet
  Then A lists B exactly once
  And a clip copied on A is applied on B exactly once

Scenario: [DISC-6] Other tailnet devices are ignored
  Given the tailnet contains a device that does not run Kopi
  Then it is never listed as a peer

Scenario: [DISC-7] The tray explains when nothing is found
  Given Kopi has been running for 30 seconds with a passphrase set
  And no peer has been found
  Then the tray status suggests checking the firewall and Tailscale
```

Discovery only reveals what mDNS already publishes: device ID, device name and protocol
version. A Kopi node answers an unauthenticated "who are you?" request on its TCP port
with exactly that; everything else requires the passphrase.

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

The setting belongs to the device where the text is copied: a sensitive copy leaves a
device only if *that* device has "Send passwords copied on this device" on. Receivers
apply whatever arrives. (rc1 testing showed this must be explicit in the UI.)

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
    | Recent clips (submenu, v0.2)  |
    | Pause sync (toggle)           |
    | Include sensitive content     |
    | Set passphrase…               |
    | Quit                          |

Scenario: [TRAY-2] Pausing stops both directions
  Given sync is paused on A
  When I copy on A, or a peer sends to A
  Then nothing is sent from A and A's clipboard is not changed by peers
```

### Feature: Recent clips

Added in v0.2, so that I can get back a clip I synced a moment ago after copying
something else over it, without copying it again on the source device.

A **synced clip** is one this device sent to its peers, or one it received from a peer
and put on its clipboard. Each device keeps its own list, in memory only. It holds at
most 5 entries, newest first.

```gherkin
Scenario: [RECENT-1] A clip sent from this device is listed
  Given A and B are syncing
  When I copy "hello" on A
  Then within 1 second A's "Recent clips" submenu shows "hello — this device — just now"
    as its first entry

Scenario: [RECENT-2] A clip received from a peer is listed
  Given A (named "Office PC") and B are syncing
  When I copy "hello" on A
  Then within 1 second B's "Recent clips" submenu shows "hello — Office PC — just now"
    as its first entry

Scenario: [RECENT-3] Newest first, at most 5 entries
  Given A and B are syncing
  When I copy "c1", "c2", "c3", "c4", "c5" and "c6" on A, in that order
  Then A's "Recent clips" lists exactly "c6", "c5", "c4", "c3", "c2", in that order

Scenario: [RECENT-4] The same text is listed once, at the top
  Given A and B are syncing
  When I copy "x", then "y", then "x" again on A
  Then A's "Recent clips" lists exactly "x", "y", in that order

Scenario: [RECENT-5] Clicking an entry restores it and syncs it as a fresh copy
  Given A and B are syncing
  And A's "Recent clips" lists "y", "x"
  When I click "x" in A's "Recent clips"
  Then within 1 second A's clipboard contains "x"
  And within 1 second B's clipboard contains "x"
  And A's "Recent clips" lists "x — this device — just now", then "y"
  And B's "Recent clips" lists "x" first, with A's name as the source

Scenario: [RECENT-6] Restoring the clip just received still syncs it (restore is not an echo)
  Given A, B and C are syncing
  And C is paused and its clipboard contains "older"
  When I copy "one" on A
  And B receives "one" and I copy nothing else on B
  And I resume sync on C
  And I click "one" in B's "Recent clips"
  Then within 1 second C's clipboard contains "one"
  And B sends "one" exactly one time for that click
  And no device sends "one" back out after receiving it

Scenario: [RECENT-7] Restoring while paused only changes this device
  Given A and B are syncing
  And A's "Recent clips" lists "y", "x"
  And sync is paused on A
  When I click "x" in A's "Recent clips"
  Then A's clipboard contains "x"
  And nothing is sent from A
  And A's "Recent clips" lists "x", then "y"

Scenario: [RECENT-8] Preview text
  When a clip with the following text is synced
  Then its entry's preview is:
    | Clip text                                                     | Preview                                       |
    | hello                                                         | hello                                         |
    | "  Dear team,\r\nthe release is ready for testing today.  "   | Dear team, the release is ready for test…     |
    | 0123456789012345678901234567890123456789                      | 0123456789012345678901234567890123456789      |
    | héllo 🌍 你好                                                 | héllo 🌍 你好                                 |
    | "a\tb"                                                        | a b                                           |
    | "  \n\t  "                                                    | (blank)                                       |
  # Quoted cells use \r, \n and \t for carriage return, line feed and tab.
  # Rules: each line break ("\r\n" or "\n") and each tab becomes one space; leading and
  # trailing whitespace is trimmed; then the first 40 characters (Unicode characters, not
  # bytes) are shown, followed by "…" only if the text was longer than 40 characters.

Scenario: [RECENT-9] Source label
  Then each entry's source is:
    | Clip came from                                | Source shown                         |
    | this device                                   | this device                          |
    | a peer whose name is known (e.g. "Mac")       | Mac                                  |
    | a peer whose name is not known                | the first 6 characters of its ID     |

Scenario: [RECENT-10] Age label
  Then each entry's age, measured from when this device sent or applied it, is:
    | Age                    | Shown        |
    | less than 60 seconds   | just now     |
    | 1 to 59 minutes        | N min        |
    | 1 to 23 hours          | N h          |
    | 24 hours or more       | N d          |
  And N is rounded down (e.g. 119 seconds shows "1 min")
  And the labels are updated at least once every 60 seconds while Kopi is running

Scenario: [RECENT-11] Empty list
  Given no clip has been synced since Kopi started
  Then "Recent clips" shows one disabled item "No recent clips"
  And its "Clear" item is disabled

Scenario: [RECENT-12] Clear empties the list on this device only
  Given A and B are syncing
  And A's and B's "Recent clips" both list "x"
  And A's clipboard contains "x"
  When I click "Clear" in A's "Recent clips"
  Then A's "Recent clips" shows "No recent clips"
  And A's clipboard still contains "x"
  And B's "Recent clips" still lists "x"

Scenario: [RECENT-13] Changing the passphrase empties the list
  Given A's "Recent clips" lists "x"
  When I set a new passphrase on A
  Then A's "Recent clips" shows "No recent clips"

Scenario: [RECENT-14] The list is never saved
  Given A's "Recent clips" lists "find-me-123"
  When I quit Kopi on A and start it again
  Then A's "Recent clips" shows "No recent clips"
  And "find-me-123" does not appear in any file in Kopi's config folder (including kopi.log)

Scenario: [RECENT-15] Pausing keeps the list
  Given A's "Recent clips" lists "x"
  When I pause sync on A and then resume it
  Then A's "Recent clips" still lists "x"

Scenario Outline: [RECENT-16] Clips that did not sync are not listed
  Given A and B are syncing
  And <situation>
  When I copy "not-synced" on A
  Then "not-synced" is not in A's "Recent clips"
  And "not-synced" is not in B's "Recent clips"

  Examples:
    | situation                                                         |
    | sync is paused on A                                               |
    | A has no passphrase set                                           |
    | "not-synced" is more than 15 MB (see SIZE-1)                      |
    | "not-synced" is sensitive and A's sensitive setting is off        |

Scenario: [RECENT-17] Clips that a receiver does not apply are not listed
  Given A and B are syncing
  When B receives a clip it does not apply (an older clip that lost under SYNC-6,
    a replayed message under SEC-3, or any clip while B is paused)
  Then that clip is not in B's "Recent clips"

Scenario: [RECENT-18] Sensitive clips are never listed on the sending device
  Given "Send passwords copied on this device" is on for A
  When I copy a password from a password manager on A
  Then B receives it (SENS-2)
  And it is not in A's "Recent clips"

Scenario: [RECENT-19] Sensitive clips are never listed on the receiving device
  Given A and B both run v0.2
  And "Send passwords copied on this device" is on for A
  When I copy a password from a password manager on A
  Then within 1 second B's clipboard contains it
  And it is not in B's "Recent clips"

Scenario: [RECENT-20] v0.1 peers keep syncing with v0.2 devices
  Given A runs v0.2 and B runs v0.1, with the same passphrase
  When I copy "to old" on A, and later "from old" on B
  Then B's clipboard contains "to old" and A's clipboard contains "from old"
  And A's "Recent clips" lists "from old", then "to old"

Scenario: [RECENT-21] Sensitive clips from a v0.2 device still reach v0.1 peers
  Given A runs v0.2 with "Send passwords copied on this device" on
  And B runs v0.1 with the same passphrase
  When I copy a password from a password manager on A
  Then B's clipboard contains it (SENS-2 is unchanged for v0.1 peers)
  And it is not in A's "Recent clips"

Scenario: [RECENT-22] Clips from v0.1 peers are treated as not sensitive
  Given A runs v0.2 and B runs v0.1 with its sensitive setting on
  When I copy a password from a password manager on B
  Then A's clipboard contains it
  And it is listed in A's "Recent clips"
  # Known limitation: a v0.1 device cannot say a clip was sensitive. Upgrade every
  # device to v0.2 to keep passwords off every Recent clips list.
```

Restoring and loops: SYNC-4 forbids a device from *automatically* re-sending a clip it
just received. Clicking a Recent clips entry is a deliberate new copy by the user, so it
is sent once as a new clip with a new timestamp, even when it is the clip this device
just received (RECENT-6). The clipboard change caused by the restore must not be sent a
second time. SYNC-5 is unchanged.

Clip text in the list is kept in memory only. Like the rest of Kopi, it is never
written to disk or to the log (log sizes and hashes only).

**Non-goals (Recent clips)**

- Saving the list to disk, or keeping it across a restart.
- More or fewer than 5 entries, or a setting to change that number.
- Syncing the list itself between devices (each device has its own list).
- Search, pinning, editing or deleting single entries.
- Showing the full clip text (e.g. as a tooltip or preview window).
- Listing clips copied locally that did not sync.
- Images, files or rich text (still plain text only).

### Feature: Configuration

- Config file in the OS user config dir (`%APPDATA%\kopi\kopi.yml`, `~/Library/Application Support/kopi/kopi.yml`).
- Holds: device name (defaults to hostname), passphrase, include-sensitive flag, port.
- The passphrase is never logged and never committed to the repo.

## Technical constraints

- Go, single binary per OS. Build with `go build ./...`; test with `go test ./...`.
- Clipboard, tray and network access sit behind interfaces so core logic is testable with fakes.
- Discovery via mDNS/DNS-SD (service `_kopi._tcp`), plus the tailnet's device list
  (`tailscale status --json`) when the Tailscale CLI is available. Tailnet candidates
  are confirmed with a TCP hello on the Kopi port before they are listed.
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
