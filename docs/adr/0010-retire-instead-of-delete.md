---
status: accepted
date: 2026-11-04
---

# Issues are retired, never deleted

There's no hard delete. `tess retire` sets a `retired_at` timestamp; null means live. A retired issue is gone from every list, the readiness graph, and partial-ID resolution, but `tess show <exact ID>` still finds it and `tess restore` clears the timestamp. This turns deletion into an ordinary field change for sync, instead of something the merge has to infer from an issue's absence.

## Rules

- All reads go through one `live_issues` view; only store writes and sync touch the base table. The retired filter exists in exactly one place.
- Links to retired issues are ignored by readiness, so dependents unblock; `retire` lists them as newly unblocked.
- `retired_at` merges as its own field: edits elsewhere never revive a retired issue. Only an explicit `restore` does.
- Retired is not `omit`. Omit is real work that was dropped and stays in history; retired means the issue shouldn't have existed (mistake, duplicate, test).

## Considered options

- Hard delete, with sync detecting deletes as "in the merge base, absent on one side": works, but resurrection bugs live in exactly that inference, and a mistaken delete can't be undone.
