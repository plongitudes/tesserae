# Pattern: autonomous build loops with locked acceptance suites and adversarial simulation

A way to hand a build to autonomous agent loops (Ralph-style: an agent re-run on the same prompt and plan until done) without getting code that passes weak tests and is still wrong. First worked out for tesserae, 2026-11-09 ([build-plan.md](design/build-plan.md)).

## 1. Decide everything before the loop

Settle every design decision first and write it down (ADRs, a requirements doc, a CLI/API surface, a glossary). A loop that meets an open question will answer it itself, quietly and inconsistently. "Decided" means recorded where the loop reads it, not just agreed in chat.

## 2. Split the build at the hard-to-reverse format

Run one loop per milestone, with a human checkpoint between them. Put the split just before whatever freezes shapes on disk or across machines (a sync format, a wire protocol, a public API, a schema other systems read). Before it, design mistakes found by using the thing are cheap fixes; after it, they're migrations. At the checkpoint: use the milestone for real, fix the design, then write the next loop's spec against what was actually built.

Don't run one loop over everything: drift and review burden grow with spec size, and the hard parts get the weakest tests.

## 3. Write the acceptance suite before the loop, and lock it

- Write it before any loop runs, and have a human review it. It's the loop's only source of "you're wrong".
- Make it strict: exact machine-readable output (JSON shapes, error codes), not "output contains X".
- The loop may add its own tests but may never edit the acceptance suite (keep it in its own directory and say so in the loop prompt). Under pressure, a loop will sometimes "fix" a failing test instead of the code.

## 4. For the subtle parts, simulate, and let an agent attack

Some behaviour only fails across interleavings over time: sync, concurrency, merges, retries, distributed state. Ordinary example-based tests miss it, and a loop will pass them with wrong code. For those:

- **Simulator (property-based testing).** Run random operation sequences against 2–3 simulated actors, interleave them in random orders, and check invariants after every run (e.g. all replicas converge; deleted things stay deleted; no edit is lost unless two edits truly conflicted). Keep the core logic a pure function so the simulator can call it directly. Prior art: QuickCheck, Hypothesis, deterministic simulation testing (FoundationDB, TigerBeetle, Antithesis), model checking (TLA+ at AWS).
- **Red-team agent.** One agent, told explicitly to break the spec, invents nasty scenarios and writes them directly as simulator test cases (operations per actor plus an interleaving). Every scenario stays as a permanent regression test.
- **Don't let agents track state.** Agents roleplaying the actors and narrating step by step drift by step 12 and tend to agree the spec works. The agent contributes imagination; the simulator contributes precision.
- **Keep it offline.** Use in-memory fakes for pure logic and local stand-ins for external systems (e.g. bare git repos in temp directories as "remotes"), so the suite is fast, deterministic, and runs anywhere.

## Checklist

1. All decisions recorded where the loop will read them.
2. Milestones split just before the hard-to-reverse format, with a human checkpoint at each.
3. A strict acceptance suite per milestone, human-reviewed, locked from the loop.
4. For subtle behaviour: invariants defined, a simulator, red-team scenarios as tests, all offline.
