# Build plan

How v1 gets built. Agreed 2026-11-09.

## Two Ralph loops with a human checkpoint

1. **Loop 1: core.** Types, the SQLite store (readiness views, event log, content-addressed texts), ID generation, every command in [cli.md](cli.md) except `sync`, `import` from old `bd` JSONL, and the agent integration ([ADR-0019](../adr/0019-agent-context-integration.md); its hooks call `tess sync`, which is a no-op until loop 2). Has its own acceptance suite.
2. **Checkpoint.** Dogfood the core on one machine. Fix the design where use shows it's wrong, while that's still cheap: the sync ref format (ADR-0011) persists on remotes and other machines, so it's the hardest thing to change later.
3. **Loop 2: sync.** Written against the core as built, with its own spec and acceptance suite (below).

### Running loop 1

Everything the loop runs on is in `scripts/ralph/`: `ralph.sh` (the host loop: worktree, offline module cache, sandbox, per-iteration verification), `prompt.md` (what each fresh iteration is told), `plan.md` (the ordered items the loop checks off, plus its notes to the human), and `check.sh` (the gate: gofmt, vet, unit tests, build, acceptance report). `ralph.sh --probe` smoke-tests the sandbox first. Dependencies are fixed in advance ([ADR-0020](../adr/0020-v1-dependencies.md)). The loop may not touch `acceptance/`, `docs/`, `go.mod`, or its own scripts; the host stops it if it does.

One big loop was rejected: sync bugs are subtle and only appear across machines over time, so a loop would pass weak tests with wrong code; sync would freeze core shapes before they're proven; and drift and review burden grow with spec size.

### Running loop 2

Same machinery, from `main` on `loop/2-sync` (`RALPH_LOOP=2` is the default now; the loop-1 plan is kept as `scripts/ralph/plan-loop1.md`). The spec is [sync.md](sync.md); the suite's loop-2 section, the two new scripts and the simulator in `acceptance/sim/` are locked like the rest. `internal/sync/sync.go` starts as the exported API with nothing behind it, so the tree builds from the first iteration. `check.sh` runs the simulator `-short` per iteration and in full under `--strict`. Added 2026-11-20.

## Sync testing (loop 2's acceptance suite)

- **Simulator.** Property-based tests run random operation sequences on 2–3 simulated machines, sync in random orders, and check invariants after every run: all machines converge to the same state; nothing retired comes back; no edit is lost unless two edits truly conflicted. Prior art: property-based testing (QuickCheck, Hypothesis) and deterministic simulation testing (FoundationDB, TigerBeetle).
- **Red-team scenarios.** One agent, told to break the spec, writes adversarial scenarios directly as simulator test cases (operations on each machine plus a sync order). The simulator, not an agent, tracks state. Every scenario stays as a regression test.
- **No network.** Merge tests call the pure merge function in memory. End-to-end tests use bare git repos in temp directories as remotes, so fetch, push, rejected pushes, and retries behave for real offline. The only network traffic in the design is a real `tess sync` against the user's git remote.
