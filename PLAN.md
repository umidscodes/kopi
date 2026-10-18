# Kopi — Build Plan (v1)

How we get from `SPEC.md` to a working app. Each milestone ends in a state that builds,
passes tests, and is committed. Review the plan before code; review each milestone before
the next one starts.

Who does what:
- **Claude** — design-heavy or risky work: crypto, sync engine, OS clipboard code, reviews.
- **Qwen (omp)** — scoped tasks with clear inputs and tests: config, CI, scripts, docs, extra tests.
- **You** — review plans and diffs, run manual checks on real hardware, decide open questions.

## Architecture

```
            ┌──────────── cmd/kopi (wiring) ────────────┐
            │                                           │
 clip.Clipboard ──► sync.Engine ◄──► proto (encrypt/frame) ◄──► transport (TCP)
   (OS impl           │   ▲                                         ▲
    or fake)          │   └── discovery (mDNS or fake) ── peers ────┘
                      ▼
                  tray (status, toggles)          config (kopi.yml)
```

Core packages (`sync`, `proto`, `config`) never import OS-specific code. Everything they
touch is an interface, so the whole sync behavior is testable in CI with fakes.

## Key design decisions

| Topic | Decision | Why |
|---|---|---|
| Transport | TCP, one connection per message, default port 47800 | Simple; clips are rare and small |
| Framing | 4-byte length prefix; reject length > 15 MiB + overhead **before** reading the body | Spec: never read oversized messages into memory |
| Key | Argon2id(passphrase) → 32-byte key, derived once at startup | Memory-hard: slows passphrase guessing |
| Encryption | XChaCha20-Poly1305, random 24-byte nonce per message | Authenticated, vetted, nonce misuse-resistant |
| Message | `{id, from, sentAt, hash, text}` inside the ciphertext | Nothing readable on the wire |
| Replay | Reject if `sentAt` is outside ±2 min, or `id` already seen | Spec: replayed messages ignored |
| Wrong passphrase | Decryption fails → peer marked "passphrase mismatch" | No key fingerprint published, so nothing to brute-force offline |
| Loop prevention | Remember hash of the last clip *applied from a peer*; a local change with that hash is not re-sent | Spec: no loops |
| Conflicts | Last writer wins on `sentAt` | Spec; clock skew between your own devices is small |
| Discovery | mDNS service `_kopi._tcp`, TXT: device id, name, version | Spec: auto-discover on LAN |
| Clipboard (Windows) | Win32 via `golang.org/x/sys/windows`, sequence-number watch, sensitive-format check | No cgo on Windows |
| Clipboard (macOS) | cgo + small Objective-C shim for `NSPasteboard` changeCount + types | Only way to read concealed/transient types |
| Tray | `fyne.io/systray` | Cross-platform, small |

## Milestones

### M0 — Repo hygiene and CI
- `.gitattributes` (LF everywhere), README stub, GitHub Actions: `go vet` + `go test` on
  `windows-latest` and `macos-latest`.
- Owner: Claude (now). Done when: workflow file exists and runs green once the repo is on GitHub.

### M1 — Core logic with fakes (no network, no OS)
- `internal/config`: load/save `kopi.yml`, defaults, never log the passphrase. **Owner: Qwen**
- `internal/proto`: key derivation, seal/open, framing, size limit, replay cache. **Owner: Claude**
- `internal/sync`: engine with loop prevention, dedupe, last-writer-wins, pause, sensitive
  and size rules. **Owner: Claude**
- `internal/clip` interface + `FakeClipboard`. **Owner: Claude**
- Tests mapped to scenario IDs (`SYNC-*`, `SEC-*`, `SENS-*`, `SIZE-*`).
- Done when: every non-OS scenario has a passing test against fakes.

### M2 — Network
- TCP transport and mDNS discovery behind interfaces. **Owner: Claude, tests: Qwen**
- Integration test: 3 engines on localhost with fake clipboards, real TCP + crypto.
  This covers most of the "Docker multi-peer lab" idea without Docker.
- Done when: `DISC-*` and multi-peer `SYNC-*` scenarios pass locally and in CI.
- Status: done. Discovery uses `brutella/dnssd` (browse with add/remove callbacks;
  sends goodbye packets on clean shutdown). Each send gets a 1-byte ack so the sender
  can tell "passphrase mismatch" from "unreachable".
- Real-mDNS test is opt-in (`KOPI_MDNS_TEST=1`).

### M3 — Real clipboards
- Windows implementation + sensitive detection. **Owner: Claude**
- macOS implementation (cgo) + sensitive detection. **Owner: Claude**
- Manual check on each OS: copy/paste, password manager copy is skipped.
- Status: done, verified on the real PC (Windows 11) and Mac (macOS 27.2).
  - Both clipboards poll a change counter (Windows sequence number, macOS changeCount).
  - Discovery: dnssd's browser queries only once, so we re-query every 5 s and expire
    silent peers after 15 s. On macOS, dnssd's own socket never receives multicast from
    other hosts (mDNSResponder owns 5353), so macOS uses native Bonjour (`dns_sd.h`).
    PC ↔ Mac discovery verified both ways with `tools/mdnsprobe`.
  - AVG on the PC sandboxes new unsigned executables and its firewall blocks inbound
    traffic; `bin/` needs an AVG exception and the app an AVG firewall allow rule.
- Carry into M4: the Mac app must be an `.app` bundle with `NSLocalNetworkUsageDescription`
  and `NSBonjourServices = _kopi._tcp` in Info.plist so macOS can ask for Local Network
  permission; first-run docs must cover AVG / Windows Firewall allow rules.

### M4 — Tray app and wiring
- `cmd/kopi` wires everything; tray menu per spec. **Owner: Claude, review: you**
- `kopi passphrase` CLI command to set the passphrase (see open question 1).
- Design as built:
  - Passphrase entry: native masked dialogs (macOS `osascript`, Windows credential
    prompt), asked twice, min 8 characters. A terminal prompt is used when `kopi
    passphrase` runs in a terminal (macOS). Windows tray apps can't use the terminal
    because shells don't wait for GUI-subsystem programs.
  - The running app polls its config file every 2 s, so `kopi passphrase` applies live.
  - Config is saved on first run so the device ID persists across restarts.
  - Tray icon is drawn in code (`internal/icon`, written by Qwen): template PNG for the
    macOS menu bar, ICO for Windows.
  - `scripts/build-windows.ps1` → `bin\kopi.exe` (no console window);
    `scripts/build-mac-app.sh` → `bin/Kopi.app` with Bonjour + Local Network keys.

### M5 — Ship it (to yourself)
- Release workflow: on tag `v*`, build Windows + macOS binaries and attach to a GitHub release. **Owner: Qwen**
- Start-at-login scripts: launchd plist (Mac), Startup shortcut / Task Scheduler (Windows). **Owner: Qwen**
- README: setup on each OS, Gatekeeper bypass, firewall prompt. **Owner: Qwen**
- Manual run of the "Known risks" table on your PC and Mac. **Owner: you**

## Resolved questions

1. **Setting the passphrase:** native dialogs instead of opening a terminal (see M4).
2. **GitHub username:** `umidscodes`; module path `github.com/umidscodes/kopi`.
