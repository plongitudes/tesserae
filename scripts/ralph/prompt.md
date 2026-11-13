# Ralph iteration: tesserae loop 1

You are one iteration of an unattended build loop. You have a fresh context; everything you need is in this repo and the branch history. Work exactly ONE plan item: the one named at the end of this prompt. When you're done, this process ends and the next iteration starts fresh.

Your Bash runs in a sandbox: you can write only inside this worktree, `$TMPDIR`, and `$RALPH_CACHE`, and you have no network. Go runs offline (`GOPROXY=off`, `GOFLAGS=-mod=readonly`, module cache pre-filled). Don't run `go get` or `go mod tidy`, and don't add a dependency: `go.mod` is fixed ([ADR-0020](../../docs/adr/0020-v1-dependencies.md)). If the item truly needs something else, abort (see 4).

## The repo

- `docs/requirements.md`, `docs/adr/` (every settled decision; never contradict one), `docs/design/cli.md` (the command surface), `docs/design/module-map.md` (packages and seams), `GLOSSARY.md` (use its terms in code, help text, and output).
- `acceptance/README.md` is the behavioural contract: exact JSON shapes, error codes, and the suite's own decisions. `acceptance/testdata/*.txtar` are the scripts. Where the README and the design docs disagree, the design docs win.
- `scripts/ralph/plan.md`: the ordered plan. Items are checked off in order. Its "Notes" section is where iterations leave notes for the human and for later iterations.
- Code goes under `cmd/tess` and `internal/` (`cli`, `tracker`, `ids`, `store/sqlite`, per the module map). The stub in `internal/cli/cli.go` is yours to replace.
- `REWRITE_HANDOFF.md` names files in the old tool worth reading for ideas: ID generation, partial-ID resolution, the SQLite schema and recursive queries. The old repo is at `../beads` if present (read-only for you). Port ideas and hard-won details, not structure; everything about its daemon, JSONL sync, and Gastown is out.

**Locked.** Never edit, delete, or add anything under `acceptance/`, `docs/`, `GLOSSARY.md`, `LOOP_PATTERN.md`, `REWRITE_HANDOFF.md`, `go.mod`, `go.sum`, or the three scripts in `scripts/ralph/` other than `plan.md`. The host checks this and stops the loop if you do. If an acceptance script or a design doc looks wrong, abort and say exactly what and why; a human fixes it.

## 1. Load the item

- Read `scripts/ralph/plan.md` in full: the item you're on, the items before it (they're built), the ones after it (don't do their work), and the Notes.
- Read the docs the item points at, and run `git log --oneline` to see what earlier iterations landed.
- Run `scripts/ralph/check.sh` once to see the current state: which acceptance scripts pass already.

## 2. Do the work

- Test-first at the seam the item names: with the `tdd` skill, write the failing unit test (store tests against SQLite `:memory:`, tracker tests through the `Tracker` interface), watch it fail, make it pass, repeat. Unit tests live next to the code they test and must not skip.
- Run single acceptance scripts while you work: `go test -count=1 -run 'TestAcceptance/<name>' ./acceptance/` (`<name>` is the `.txtar` file name without the extension). They are the item's definition of done when the item names them. A script that fails on a step belonging to a later item is fine; note which step in your commit message.
- Stay inside the item. If you find work outside it, add a line under "Discovered" in `plan.md`'s Notes and keep going. Don't change items you aren't on.
- One code path per command: `cli` is an adapter over `tracker`; readiness comes from the `blocked_issues` view and nowhere else ([ADR-0007](../../docs/adr/0007-readiness-is-one-sql-view.md)); every write diffs its typed patch into events inside the same transaction ([ADR-0013](../../docs/adr/0013-field-level-event-log.md)).
- Don't monkeypatch or hack around something to get green: no special-casing a test's input, no hard-coded output, no `t.Skip`, no editing the suite. If the honest path is blocked, abort (see 4).

## 3. Verify and commit

- `scripts/ralph/check.sh` must end with `=== ALL CHECKS PASS`. It runs `gofmt -l`, `go vet`, the unit tests (nothing skipped), builds `bin/tess`, and reports the acceptance suite script by script. The acceptance scripts the item names must now be in the PASS list.
- In `plan.md`, change the item's `- [ ]` to `- [x]` and, under Notes, add 3–8 bullets for the item: key decisions, deviations from the docs (there should be none; if there are, say so plainly), gotchas, anything the human should look at.
- Then add 1–4 bullets under "Reflections" in the same Notes section: where the docs or the suite were ambiguous or felt wrong and how you resolved it, anything you did that you're not confident about, anything in the process (the prompt, the plan, the sandbox, the checks) that got in your way or that a later iteration should know. Be candid and specific; the human reads these to decide what to look at later. Skip only if you genuinely have nothing to add.
- Stage explicit paths only (never `git add -A` or `commit -a`). Commit on the current branch. Message:

  ```
  LOOP1(<item number>): <item title>

  <what landed and the acceptance scripts now passing: 3-8 lines>

  Co-Authored-By: Claude <noreply@anthropic.com>
  ```

## 4. Finish or abort

- Success: everything in 3 is done and committed, the tree is clean (`git status --porcelain` prints nothing). Print `<promise>DONE</promise>`.
- Blocked (a locked file looks wrong, the item needs a dependency or network, the sandbox denies something you need, or you can't make the tests honest): don't commit. Write what blocked you, what you tried, and what you recommend as the last thing before the tag, and print `<promise>ABORT</promise>`. The human reads it from the log.

Print exactly one promise tag, as the last thing you output.
