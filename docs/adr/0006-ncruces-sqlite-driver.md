---
status: accepted
date: 2026-11-03
---

# SQLite driver: ncruces/go-sqlite3 v0.36 or later

We use `github.com/ncruces/go-sqlite3` at v0.36+, without importing its `embed` package (it's unnecessary from v0.36 and prints a warning). Versions before v0.36 run SQLite on the wazero WebAssembly runtime and compile it at every process start: v0.30.4, which the beads fork pins, spends ~460 ms opening a database, against ~5.6 ms for v0.36. That start-up cost may be part of why the old tool needed a daemon.

## Considered options

- `mattn/go-sqlite3` (cgo): ~4 ms faster on a `ready` query, but cgo breaks the single static binary and easy cross-compiling.
- `modernc.org/sqlite` (C transpiled to Go): within a few ms of ncruces; the fallback if ncruces stalls.

Evidence: [spike write-up](../spikes/2026-11-03-readiness-and-startup.md).
