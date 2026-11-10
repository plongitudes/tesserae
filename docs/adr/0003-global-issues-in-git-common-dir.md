---
status: accepted
date: 2026-11-02
---

# Issues are global, and the database lives in git's common directory

Issues aren't branch-scoped: that's how the old tool actually behaved, and storing global data in branch-tracked files caused most of its sync and worktree bugs. The SQLite database lives at `git rev-parse --git-common-dir`, so every worktree sees the same database by construction, with no redirect files or worktree detection.

## Consequences

- The database is never committed and never appears in a working tree; sharing it across machines is the sync layer's job ([ADR-0004](0004-sync-over-unchecked-out-git-ref.md)).
- Parallel agents in separate worktrees write to one database, so the store must handle concurrent writers (WAL, `BEGIN IMMEDIATE` with busy retry).
