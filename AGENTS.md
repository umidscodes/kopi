# AGENTS.md

Instructions for coding agents (omp, Claude Code) working on Kopi.

## Source of truth

- `SPEC.md` defines behavior. If code and spec disagree, the spec wins. If the spec is
  ambiguous or wrong, stop and ask; do not silently pick an interpretation.
- Every change should map to one or more scenarios in `SPEC.md`.

## Commands

- Build: `go build ./...`
- Test: `go test ./...`
- Race test: `go test -race ./...` (needs CGO; skip on Windows if no C toolchain)
- Vet: `go vet ./...`
- Format: `gofmt -w .`

Run build, vet and tests before saying a task is done, and show their output.

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
