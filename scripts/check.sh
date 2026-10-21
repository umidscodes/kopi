#!/bin/sh
# The one check command: format, vet, build, test, race. Same steps as CI.
# Usage: sh scripts/check.sh [--quick]    (--quick skips the race step)
# Never installs anything, needs no network, changes no files.

quick=0
for arg in "$@"; do
  case "$arg" in
    --quick) quick=1 ;;
    *) echo "unknown argument: $arg (use --quick)" >&2; exit 2 ;;
  esac
done

cd "$(dirname "$0")/.." || exit 1

fail() {
  echo "FAIL $1"
  exit 1
}

echo "==> gofmt"
unformatted=$(gofmt -l .) || fail gofmt
if [ -n "$unformatted" ]; then
  echo "Not gofmt'd:"
  echo "$unformatted"
  fail gofmt
fi
echo "PASS gofmt"

for step in "go vet ./..." "go build ./..." "go test ./..."; do
  echo "==> $step"
  $step || fail "$step"
  echo "PASS $step"
done

if [ "$quick" -eq 1 ]; then
  echo "SKIP race: --quick"
  exit 0
fi

# Race needs cgo: CGO_ENABLED=1 and a C compiler that exists on PATH.
have_cc=0
cc=$(go env CC) || fail "go env CC"
cc=${cc%% *}
if [ "$(go env CGO_ENABLED)" = "1" ] && [ -n "$cc" ] && command -v "$cc" >/dev/null 2>&1; then
  have_cc=1
fi
if [ "$have_cc" -eq 0 ]; then
  echo "SKIP race: no C toolchain (see AGENTS.md)"
  exit 0
fi
echo "==> go test -race ./... (cc: $cc)"
go test -race ./... || fail "go test -race ./..."
echo "PASS go test -race ./..."
