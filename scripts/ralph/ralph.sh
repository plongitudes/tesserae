#!/usr/bin/env bash
# Ralph loop for tesserae (docs/design/build-plan.md): one fresh, sandboxed
# `claude -p` per plan item, in a worktree on an integration branch, until
# scripts/ralph/plan.md has no unchecked item and the locked acceptance suite
# passes. Adapted from super_rando's Scripts/ralph. Loop 1 (the core) ran
# from design/v1 on loop/1-core; loop 2 (sync) runs from main on loop/2-sync.
#
#   scripts/ralph/ralph.sh [max-iterations]      # run the loop (default 30)
#   scripts/ralph/ralph.sh --probe               # sandbox smoke test, no plan work
#
# Environment:
#   RALPH_LOOP    the loop number, names the worktree    (default: 2)
#   RALPH_BASE    branch the loop branch is cut from     (default: main)
#   RALPH_BRANCH  the loop's integration branch          (default: loop/2-sync)
#   RALPH_MODEL   passed to `claude --model` when set
#
# Claude only commits. This script, running on the host outside the sandbox,
# checks each iteration before trusting it: the promise tag, a new commit, a
# clean tree, one more plan item checked off, nothing under the locked paths
# touched, and scripts/ralph/check.sh green. The repo has no remote, so there
# is no push step; the branch stays local for review and merge.
set -euo pipefail

probe=0
if [ "${1:-}" = "--probe" ]; then
  probe=1
  shift
fi
max=${1:-30}
loop=${RALPH_LOOP:-2}
base=${RALPH_BASE:-main}
branch=${RALPH_BRANCH:-loop/$loop-sync}

repo=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
here="$repo/scripts/ralph"
wt="$repo/../$(basename "$repo")-ralph-loop$loop"
state="$repo/.ralph" # gitignored: caches and logs
mkdir -p "$state/logs"

# Paths the loop may not change. Checked per iteration against the commit it
# started from, so a human can still edit them between runs.
locked=(acceptance docs GLOSSARY.md LOOP_PATTERN.md REWRITE_HANDOFF.md go.mod go.sum
  scripts/ralph/ralph.sh scripts/ralph/check.sh scripts/ralph/prompt.md scripts/ralph/plan-loop1.md)

# --- worktree -------------------------------------------------------------
if [ ! -d "$wt" ]; then
  git -C "$repo" show-ref --verify --quiet "refs/heads/$branch" || git -C "$repo" branch "$branch" "$base"
  git -C "$repo" worktree add "$wt" "$branch"
fi

# --- caches + dependencies ------------------------------------------------
# The sandbox has no network, so modules are fetched here on the host before
# every run, into a module cache the sandbox then reads offline. go.mod is
# locked (ADR-0020): an iteration that needs a new module aborts instead.
cache="$state/cache"
mkdir -p "$cache"
export RALPH_CACHE="$cache"
export GOMODCACHE="$cache/gomod" GOCACHE="$cache/gobuild"
(cd "$wt" && go mod download all && go build ./... && go vet ./... >/dev/null) # vet also compiles the test packages
export GOFLAGS=-mod=readonly GOPROXY=off GOTOOLCHAIN=local

# --- sandbox settings -----------------------------------------------------
# The worktree itself is writable as the working directory. The scratch dir
# holds TMPDIR (testscript and mktemp write there); the cache dir holds Go's
# module and build caches. Foundation's atomic writes stage in the per-user
# temp root regardless of TMPDIR.
scratch=$(mktemp -d -t ralph)
mkdir -p "$scratch/tmp"
trap 'rm -rf "$scratch"' EXIT
settings="$scratch/settings.json"
export TMPDIR="$scratch/tmp"
usertmp=$(getconf DARWIN_USER_TEMP_DIR)
jq -n --arg scratch "$scratch" --arg cache "$cache" --arg usertmp "$usertmp" '{
  sandbox: {
    enabled: true,
    failIfUnavailable: true,
    allowUnsandboxedCommands: false,
    autoAllowBashIfSandboxed: true,
    filesystem: {
      allowWrite: [$scratch, $cache, $usertmp],
      denyRead: ["~/.ssh", "~/.aws", "~/.gnupg"]
    },
    network: {
      allowedDomains: [],
      allowLocalBinding: false
    }
  }
}' >"$settings"

claude_run() { # <prompt> -> stream-json on stdout
  local model=()
  [ -n "${RALPH_MODEL:-}" ] && model=(--model "$RALPH_MODEL")
  (cd "$wt" && claude -p "$1" </dev/null \
    --settings "$settings" \
    --permission-mode dontAsk \
    --allowedTools "Read,Edit,Write,Bash,Glob,Grep,Skill,TodoWrite" \
    ${model[@]+"${model[@]}"} \
    --verbose --output-format stream-json)
}
stream_text='select(.type == "assistant").message.content[]? | select(.type == "text").text // empty | . + "\n"'
final_result='select(.type == "result").result // empty'

if [ "$probe" -eq 1 ]; then
  claude_run "Sandbox smoke test. Don't change any tracked file. Run each of these with Bash and report, per line, the exit status and the first line of any error:
1. go version && go env GOMODCACHE GOFLAGS GOPROXY
2. scripts/ralph/check.sh   (report its last 15 lines; acceptance failures are expected right now)
3. touch ~/ralph-sandbox-probe   (EXPECTED to fail)
4. curl -sS --max-time 5 https://example.com -o /dev/null   (EXPECTED to fail)
5. go test -count=1 -run 'TestAcceptance/init' ./acceptance/   (report pass or fail and the first failing line)
6. git status --short && git commit --allow-empty -m 'RALPH probe' && git reset --soft HEAD~1
Then summarise which of 1, 2, 5, 6 failed and why." | jq --unbuffered -rj "$stream_text"
  exit 0
fi

# --- loop -----------------------------------------------------------------
if [ -n "$(git -C "$wt" status --porcelain)" ]; then
  echo "=== $wt has uncommitted changes from an interrupted run; clean it up first" >&2
  exit 1
fi

plan="$wt/scripts/ralph/plan.md"
unchecked() { grep -cE '^- \[ \]' "$plan" || true; }
checked() { grep -cE '^- \[x\]' "$plan" || true; }
finished() { (cd "$wt" && "$here/check.sh" --strict >/dev/null 2>&1); }

for ((i = 1; i <= max; i++)); do
  if [ "$(unchecked)" -eq 0 ] && finished; then
    echo "=== plan complete and acceptance suite green: loop $loop done after $((i - 1)) iterations"
    (cd "$wt" && "$here/check.sh" --strict | tail -3)
    exit 0
  fi
  next=$(grep -m1 -E '^- \[ \]' "$plan" | sed -E 's/^- \[ \] //' || true)
  if [ -n "$next" ]; then
    item="Plan item for this iteration: $next"
  else
    item="Every plan item is checked, but the acceptance suite still fails. Run scripts/ralph/check.sh, pick one failing script, add a plan item for the fix, check it off when done."
  fi
  echo "=== iteration $i: ${next:-fix acceptance failures}"
  head_before=$(git -C "$wt" rev-parse HEAD)
  checked_before=$(checked)
  log="$state/logs/$(date +%Y%m%d-%H%M%S)-$i.jsonl"

  claude_run "$(cat "$here/prompt.md")

$item" | tee "$log" | jq --unbuffered -rj "$stream_text"
  result=$(jq -r "$final_result" "$log")

  if [[ "$result" != *"<promise>DONE</promise>"* ]]; then
    echo "=== iteration $i did not finish (log: $log); stopping"
    exit 1
  fi
  # trust, but verify on the host
  [ "$(git -C "$wt" rev-parse HEAD)" != "$head_before" ] || { echo "=== iteration $i committed nothing; stopping"; exit 1; }
  [ -z "$(git -C "$wt" status --porcelain)" ] || { echo "=== iteration $i left uncommitted changes; stopping"; exit 1; }
  if ! git -C "$wt" diff --quiet "$head_before" HEAD -- "${locked[@]}"; then
    echo "=== iteration $i changed a locked path; stopping"
    git -C "$wt" diff --stat "$head_before" HEAD -- "${locked[@]}"
    exit 1
  fi
  [ "$(checked)" -gt "$checked_before" ] || { echo "=== iteration $i checked off no plan item; stopping"; exit 1; }
  (cd "$wt" && "$here/check.sh") || { echo "=== checks fail on host after iteration $i; stopping"; exit 1; }
done
echo "=== hit max iterations ($max)"
