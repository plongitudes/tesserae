# Spike: readiness placement and process start-up (2026-11-03)

Questions: should "blocked" be computed in Go or SQL, does it need a cache, and how much does SQLite cost per CLI invocation? Code is in `spikes/readiness/` and `spikes/startup/` (throwaway). Machine: Apple Silicon laptop. Fixtures mirror the old repo's `fixtures.DefaultLargeConfig`: 10K issues (10% epics, 30% features, 60% tasks via parent-child; 20% of tasks get a random `blocks` link; ~50% open; 5% deferred), plus a 20K variant. Every approach was checked to produce identical blocked sets and ready lists.

## Readiness, in-process (ncruces v0.36, 10K issues)

| Approach | `ready` all rows | `ready` limit 10 |
|---|---|---|
| Recursive SQL per query | 24.9 ms | 11.6 ms |
| Materialized cache read (+5 ms rebuild per write) | 20.8 ms | 8.3 ms |
| Load graph, compute in Go | 41.3 ms | 29.5 ms |
| Recursive SQL as a view | 25.3 ms | 11.5 ms |

Pieces: the blocked set in SQL takes 4.3 ms; loading the graph into Go takes 15 ms; computing in Go takes 0.1 ms. The cost is moving rows across the driver, not the computation. At 20K issues, everything roughly doubles.

## Whole-process start-up (10K issues, median of 40 runs)

| Driver | Start and exit | Open + `SELECT 1` | `ready` limit 10 |
|---|---|---|---|
| None (Go floor) | 4.6 ms | | |
| ncruces v0.30.4 | 4.8 ms | 462 ms | 480 ms |
| ncruces v0.36.0 | 4.2 ms | 5.6 ms | 19 ms |
| modernc | 4.6 ms | 5.9 ms | 24 ms |
| mattn (cgo) | 4.7 ms | 5.7 ms | 15 ms |

v0.30.4 depends on wazero and compiles SQLite's WebAssembly at every start; v0.36 depends on `go-sqlite3-wasm` (SQLite as translated Go) and doesn't.

## Index experiment (ncruces v0.36, whole process)

| Setup | `ready` limit 10 | `ready` all |
|---|---|---|
| Whole-graph view, no new index | 19.0 ms | 23.5 ms |
| Whole-graph view + `(priority, created_at)` index | 12.1 ms | 23.5 ms |
| Per-candidate ancestor check + index + `+i.status` hint | 6.1 ms | 39.0 ms |

Without the hint, SQLite 3.54 (bundled with ncruces) uses the status index and sorts all ~5K candidates, making the per-candidate check slower (34 ms). macOS's system SQLite 3.43 picked the index-ordered plan unaided. The two blockedness shapes agree on all 10K issues.

Outcome: [ADR-0006](../adr/0006-ncruces-sqlite-driver.md), [ADR-0007](../adr/0007-readiness-is-one-sql-view.md).
