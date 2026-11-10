# Build plan

How v1 gets built. Agreed 2026-11-09.

## Two Ralph loops with a human checkpoint

1. **Loop 1: core.** Types, the SQLite store (readiness views, event log, content-addressed texts), ID generation, every command in [cli.md](cli.md) except `sync`, `import` from old `bd` JSONL, and the agent integration ([ADR-0019](../adr/0019-agent-context-integration.md); its hooks call `tess sync`, which is a no-op until loop 2). Has its own acceptance suite.
2. **Checkpoint.** Dogfood the core on one machine. Fix the design where use shows it's wrong, while that's still cheap: the sync ref format (ADR-0011) persists on remotes and other machines, so it's the hardest thing to change later.
3. **Loop 2: sync.** Written against the core as built, with its own spec and acceptance suite (below).

One big loop was rejected: sync bugs are subtle and only appear across machines over time, so a loop would pass weak tests with wrong code; sync would freeze core shapes before they're proven; and drift and review burden grow with spec size.

## Sync testing (loop 2's acceptance suite)

- **Simulator.** Property-based tests run random operation sequences on 2–3 simulated machines, sync in random orders, and check invariants after every run: all machines converge to the same state; nothing retired comes back; no edit is lost unless two edits truly conflicted. Prior art: property-based testing (QuickCheck, Hypothesis) and deterministic simulation testing (FoundationDB, TigerBeetle).
- **Red-team scenarios.** One agent, told to break the spec, writes adversarial scenarios directly as simulator test cases (operations on each machine plus a sync order). The simulator, not an agent, tracks state. Every scenario stays as a regression test.
- **No network.** Merge tests call the pure merge function in memory. End-to-end tests use bare git repos in temp directories as remotes, so fetch, push, rejected pushes, and retries behave for real offline. The only network traffic in the design is a real `tess sync` against the user's git remote.
