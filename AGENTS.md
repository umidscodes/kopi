# AGENTS.md

Instructions for coding agents (omp, Claude Code) working on Kopi.

## Source of truth

- `SPEC.md` defines behavior. If code and spec disagree, the spec wins. If the spec is
  ambiguous or wrong, stop and ask; do not silently pick an interpretation.
- Every change should map to one or more scenarios in `SPEC.md`.
- `PLAN.md` holds the architecture, design decisions and milestones. Work only on the
  milestone or task you were given; don't start the next one.
- Scenarios have IDs like `SYNC-4` or `SEC-1`. Name tests after them and put the ID in
  the test name, e.g. `TestSYNC4_ReceivedClipIsNotRebroadcast`, so we can trace every
  scenario to its tests.

## Commands

- Build: `go build ./...`
- Test: `go test ./...`
- Race test: `go test -race ./...` (needs CGO; skip on Windows if no C toolchain)
- Vet: `go vet ./...`
- Format: `gofmt -w .`

Run build, vet and tests before saying a task is done, and show their output.
"Done" without that output does not count as done.

## Lessons learned

Mistakes agents made on this project, so they aren't repeated. Add to this list when a
review catches something new.

- An agent reported a task as complete when the output was wrong. Check results
  yourself (run it, read the file back) before reporting success.
- Fakes passed while real hardware failed (mDNS on macOS). Anything touching the OS or
  network needs an opt-in real test (`KOPI_CLIPBOARD_TEST=1`, `KOPI_MDNS_TEST=1`) run
  on both machines before the milestone counts as done.
- Verify the observer, not just the system: an agent's own shell may be sandboxed and
  see a different copy of files outside the repo. Kopi "didn't save settings" for a day
  until the user opened the file in Notepad and it was fine.
- When a test fails, verify the test harness first: a garbled prompt and two probes
  that never overlapped both looked like product bugs.
- Real networks are hostile: other apps share UDP 5353 and antivirus firewalls drop
  traffic silently. Don't rely on a single discovery path, and make the UI say what to
  check when nothing is found.
- Test helpers that feed events from several goroutines must wait for each step to be
  processed before the next, or the test races its own setup.
- Code in `_darwin.go` / `_darwin.c` can't be compiled on Windows. Push it to a branch
  and let the macOS CI job (or the Mac over SSH) build it before merging.

## Rules

- Never weaken, skip or delete a failing test to make it pass. Fix the code, or report
  that the test looks wrong and why.
- Keep OS-specific code in files with build tags (`_windows.go`, `_darwin.go`). Core
  logic must not import OS packages directly; it talks to interfaces so it can run
  with fakes in tests.
- No hand-rolled cryptography. Use `golang.org/x/crypto` and the standard library.
- Never log clip contents or the passphrase. Log sizes and hashes only.
- No new dependencies without saying why in the task summary.
- Small, focused commits. Do not commit secrets, binaries or local config.

## Layout (target)

```
cmd/kopi/          main package: wires everything together
internal/clip/     Clipboard interface + windows/darwin implementations + fake
internal/sync/     sync engine: dedupe, loop prevention, last-writer-wins
internal/proto/    wire format, encryption, replay protection, size limits
internal/discovery mDNS discovery behind an interface + fake
internal/tray/     tray menu
internal/config/   config file load/save
```
