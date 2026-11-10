---
status: accepted
date: 2026-11-03
---

# "Blocked" is defined once, as a whole-graph SQL view

The old tool had three definitions of "blocked" that disagreed. Here the definition lives in exactly one place: a SQL view in the store's schema (`blocked_issues`, built on a view of readiness edges). `ready`, `blocked`, the close guard, cycle detection, and "newly unblocked" all query it. There is no Go reimplementation, no materialized cache, and no in-memory store adapter; tests run against SQLite `:memory:`, so the view really is the only definition.

## Considered options

- Computing readiness in Go over a graph loaded from the store: 2–2.5x slower, because moving rows out of SQLite costs far more than the computation (0.1 ms).
- A materialized blocked cache, as the old tool had: saves ~6 ms per read, adds a rebuild on every write and invalidation rules to get wrong. The old tool's 752 ms uncached figure didn't reproduce for the reduced dependency types (~6 ms here).
- A per-candidate ancestor check plus a `(priority, created_at)` index: `ready --limit 10` drops to ~6 ms end to end, but full lists get slower (39 vs 23.5 ms) and SQLite 3.54 needs a planner hint to pick the right plan. Kept in reserve: switch without hints, with a query-plan test, if `ready` latency ever matters.

## Consequences

- `ready --limit 10` is ~19 ms end to end at 10K issues and scales roughly linearly with issue count. We've accepted that; performance is not a goal beyond "doesn't feel slow".
- A future second store (e.g. Markdown files) keeps SQLite as its index, so it inherits the view instead of reimplementing it.

Evidence: [spike write-up](../spikes/2026-11-03-readiness-and-startup.md).
