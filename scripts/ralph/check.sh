#!/usr/bin/env bash
# The gate every loop iteration must pass: gofmt, go vet, unit tests with
# nothing skipped, a build of the binary, and a per-test report on the locked
# acceptance suite (the scripts, and from loop 2 the simulator's scenarios
# and random runs). Shared by the sandboxed agent and the host-side check in
# ralph.sh so both judge the same thing.
#
#   scripts/ralph/check.sh            # unit gate; acceptance reported, not required;
#                                     # the simulator runs -short (5 random runs)
#   scripts/ralph/check.sh --strict   # acceptance must pass too, simulator in full
#                                     # (the loop's finish line)
#
# Inside the sandbox there is no network: ralph.sh fills the Go module cache on
# the host first and RALPH_CACHE points the toolchain at it.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

strict=0
[ "${1:-}" = "--strict" ] && strict=1

if [ -n "${RALPH_CACHE:-}" ]; then
  export GOMODCACHE="$RALPH_CACHE/gomod" GOCACHE="$RALPH_CACHE/gobuild"
  export GOFLAGS=-mod=readonly GOPROXY=off GOTOOLCHAIN=local
fi

out=$(mktemp "${TMPDIR:-/tmp}/ralph-check.XXXXXX")
fail() { # <label> <grep pattern>
  grep -E "$2" "$out" | head -60 || true
  echo "=== $1 (full log: $out)"
  exit 1
}

# --- formatting, vet, unit tests, build -----------------------------------
unformatted=$(gofmt -l cmd internal acceptance)
if [ -n "$unformatted" ]; then
  echo "$unformatted"
  echo "=== GOFMT FAIL (run gofmt -w on the files above)"
  exit 1
fi
go vet ./... >"$out" 2>&1 || fail "GO VET FAIL" "."
go test -v -count=1 ./cmd/... ./internal/... >"$out" 2>&1 || fail "GO TESTS FAIL" "^(--- FAIL|FAIL|panic:|.*_test.go:[0-9]+:)"
# A skipped test means its seam didn't run.
if grep -E "^[[:space:]]*--- SKIP" "$out"; then
  echo "=== GO TESTS SKIPPED (full log: $out)"
  exit 1
fi
unit=$(grep -cE "^[[:space:]]*--- PASS" "$out" || true)
mkdir -p bin
go build -o bin/tess ./cmd/tess >"$out" 2>&1 || fail "GO BUILD FAIL" "."

# --- acceptance (locked suite) --------------------------------------------
short=(-short)
[ "$strict" -eq 1 ] && short=()
set +e
go test -v -count=1 "${short[@]}" ./acceptance/... >"$out" 2>&1
acc_status=$?
set -e
if ! grep -qE "^[[:space:]]*--- (PASS|FAIL): TestAcceptance/" "$out"; then
  # It didn't even run (compile error in the package under test, usually).
  fail "ACCEPTANCE DID NOT RUN" "."
fi
# One line per script, scenario and random seed; the top-level test lines
# and the per-seed noise are left out.
grep -E "^[[:space:]]*--- (PASS|FAIL): (TestAcceptance|TestScenarios)/" "$out" | sed -E 's/^[[:space:]]*--- //; s/ \(.*//' | sort -k2
grep -E "^[[:space:]]*--- (PASS|FAIL): TestRandomRunsConverge " "$out" | sed -E 's/^[[:space:]]*--- //; s/ \(.*//' || true
count() { grep -cE "^[[:space:]]*--- $1: $2/" "$out" || true; }
echo "unit: $unit tests passed; scripts: $(count PASS TestAcceptance)/$(( $(count PASS TestAcceptance) + $(count FAIL TestAcceptance) )) pass; scenarios: $(count PASS TestScenarios)/$(( $(count PASS TestScenarios) + $(count FAIL TestScenarios) )) pass; random runs: $(count PASS TestRandomRunsConverge)/$(( $(count PASS TestRandomRunsConverge) + $(count FAIL TestRandomRunsConverge) )) pass"
if grep -qE "^\[build failed\]|^FAIL[[:space:]].*\[build failed\]" "$out"; then
  echo "=== ACCEPTANCE PACKAGE DOES NOT BUILD (full log: $out)"
  exit 1
fi
if [ "$strict" -eq 1 ] && [ "$acc_status" -ne 0 ]; then
  echo "=== ACCEPTANCE FAIL (full log: $out)"
  exit 1
fi

rm -f "$out"
echo "=== ALL CHECKS PASS"
