# Ralph iteration: tesserae loop 2 (sync)

You are one iteration of an unattended build loop. You have a fresh context; everything you need is in this repo and the branch history. Work exactly ONE plan item: the one named at the end of this prompt. When you're done, this process ends and the next iteration starts fresh.

Your Bash runs in a sandbox: you can write only inside this worktree, `$TMPDIR`, and `$RALPH_CACHE`, and you have no network. Go runs offline (`GOPROXY=off`, `GOFLAGS=-mod=readonly`, module cache pre-filled). Don't run `go get` or `go mod tidy`, and don't add a dependency: `go.mod` is fixed ([ADR-0020](../../docs/adr/0020-v1-dependencies.md); git is used through the `git` binary, which is on `PATH`). Git works locally: tests make bare repositories under `$TMPDIR` and fetch and push between directories. If the item truly needs something else, abort (see 4).

## The repo

- Loop 1 built the core: every command but `sync`, in `internal/cli`, `internal/tracker`, `internal/ids`, `internal/store/sqlite`. It is done and its plan is in `scripts/ralph/plan-loop1.md` (read its Notes for how the store and tracker work). Don't rework it beyond what an item asks.
- The spec for this loop: [docs/design/sync.md](../../docs/design/sync.md), with [ADR-0021](../../docs/adr/0021-latest-event-wins.md), [ADR-0022](../../docs/adr/0022-merge-repairs-structure.md), [ADR-0023](../../docs/adr/0023-id-collisions-refuse.md), and the earlier [ADR-0004](../../docs/adr/0004-sync-over-unchecked-out-git-ref.md), [ADR-0011](../../docs/adr/0011-sync-ref-layout.md), [ADR-0012](../../docs/adr/0012-explicit-sync.md), [ADR-0013](../../docs/adr/0013-field-level-event-log.md). The command surface is in [cli.md](../../docs/design/cli.md); packages and seams in [module-map.md](../../docs/design/module-map.md); terms in `GLOSSARY.md` (use them in code, help text, and output).
- `acceptance/README.md`, section "Loop 2", is the behavioural contract: exact output shapes, codes, the hook facts, and the Go API of `internal/sync` that the simulator calls. `acceptance/testdata/sync.txtar` and `doctor.txtar` are the scripts; `acceptance/sim/` is the simulator (random runs and red-team scenarios) with its invariants in `invariants_test.go`. Where the README and the design docs disagree, the design docs win.
- `scripts/ralph/plan.md`: the ordered plan. Items are checked off in order. Its "Notes" section is where iterations leave notes for the human and for later iterations.
- `internal/sync/sync.go` is the exported API with nothing behind it; it is yours to fill in and restructure, as long as the exported names and signatures stay exactly as the README gives them. New packages go where the module map says: `internal/sync` (snapshot, merge, store, `Run`), `internal/sync/gitref` (the transport; the only place that knows ref names), `internal/git` (running the git binary).

**Locked.** Never edit, delete, or add anything under `acceptance/`, `docs/`, `GLOSSARY.md`, `go.mod`, `go.sum`, `scripts/ralph/plan-loop1.md`, or the three scripts in `scripts/ralph/` other than `plan.md`. The host checks this and stops the loop if you do. If an acceptance script, a scenario, an invariant, or a design doc looks wrong, abort and say exactly what and why; a human fixes it.

## 1. Load the item

- Read `scripts/ralph/plan.md` in full: the item you're on, the items before it (they're built), the ones after it (don't do their work), and the Notes.
- Read the docs the item points at, and run `git log --oneline` to see what earlier iterations landed.
- Run `scripts/ralph/check.sh` once to see the current state: which scripts, scenarios and random runs pass already.

## 2. Do the work

- Test-first at the seam the item names: with the `tdd` skill, write the failing unit test, watch it fail, make it pass, repeat. Unit tests live next to the code they test and must not skip. Merge tests call `Merge` on snapshots built in memory; transport tests use bare repositories under `$TMPDIR`; nothing touches the network.
- Run single acceptance tests while you work: `go test -count=1 -run 'TestAcceptance/<name>' ./acceptance/`, `go test -count=1 -run 'TestScenarios/<name with underscores for spaces>' ./acceptance/sim/`, `TESS_SIM_SEED=<n> go test -count=1 -run TestRandomRunsConverge ./acceptance/sim/`. They are the item's definition of done when the item names them. A test that fails on a step belonging to a later item is fine; note which step in your commit message.
- A failing simulator run prints the seed and every operation; read the log before touching code. If an invariant looks wrong to you, it probably isn't: it states the spec. Abort rather than weaken anything.
- Stay inside the item. If you find work outside it, add a line under "Discovered" in `plan.md`'s Notes and keep going. Don't change items you aren't on.
- The merge is a pure function and symmetric in ours and theirs; nothing in it reads the clock, the environment or the database. The sync write to the database logs no events (the events arrive with the snapshot). Only the transport knows ref names. One git process per step, never one per object. Nothing is written locally before the push succeeds ([sync.md](../../docs/design/sync.md), "What a sync is").
- Don't monkeypatch or hack around something to get green: no special-casing a test's input, no hard-coded output, no `t.Skip`, no editing the suite. If the honest path is blocked, abort (see 4).

## 3. Verify and commit

- `scripts/ralph/check.sh` must end with `=== ALL CHECKS PASS`. It runs `gofmt -l`, `go vet`, the unit tests (nothing skipped), builds `bin/tess`, and reports the acceptance suite test by test. The tests the item names must now be in the PASS list.
- In `plan.md`, change the item's `- [ ]` to `- [x]` and, under Notes, add 3–8 bullets for the item: key decisions, deviations from the docs (there should be none; if there are, say so plainly), gotchas, anything the human should look at.
- Then add 1–4 bullets under "Reflections" in the same Notes section: where the docs or the suite were ambiguous or felt wrong and how you resolved it, anything you did that you're not confident about, anything in the process (the prompt, the plan, the sandbox, the checks) that got in your way or that a later iteration should know. Be candid and specific; the human reads these to decide what to look at later. Skip only if you genuinely have nothing to add.
- Stage explicit paths only (never `git add -A` or `commit -a`). Commit on the current branch. Message:

  ```
  LOOP2(<item number>): <item title>

  <what landed and the acceptance tests now passing: 3-8 lines>

  Co-Authored-By: Claude <noreply@anthropic.com>
  ```

## 4. Finish or abort

- Success: everything in 3 is done and committed, the tree is clean (`git status --porcelain` prints nothing). Print `<promise>DONE</promise>`.
- Blocked (a locked file looks wrong, the item needs a dependency or network, the sandbox denies something you need, or you can't make the tests honest): don't commit. Write what blocked you, what you tried, and what you recommend as the last thing before the tag, and print `<promise>ABORT</promise>`. The human reads it from the log.

Print exactly one promise tag, as the last thing you output.
