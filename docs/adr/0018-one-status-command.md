---
status: accepted
date: 2026-11-09
---

# One `status` command; transitions have rules, not side effects

All status changes go through `tess status ID STATUS [-r REASON]`, replacing per-transition verbs (`start`, `stop`, `review`, `done`, `omit`, `reopen`, `hold`, `resume`). The settable values are `ready` (not started: put it in the queue), `in-progress`, `pending-review`, `held`, `done`, and `omit`. `ready` is stored; it displays as `waiting` whenever the issue is blocked or has open children, derived from the readiness view, so the output says "shows as waiting: blocked by …" when that happens.

Transitions are validated in the store, but they never change anything except status and its reason:

- `held` and `omit` require a reason; `done` and `omit` aren't valid targets for `held`.
- `done`/`omit` are refused while the issue has open children ([ADR-0016](0016-hierarchy-rules.md)); `done` is refused while the issue is blocked, unless `--force`.
- `done`, `omit`, and retire print the issues they newly unblock (informational, not an action).
- No assignee changes or other side effects. Coupling actions to transitions (e.g. clearing the assignee when work stops) is left for a future hook mechanism.

`show` displays the previous status of a held issue ("held (was pending-review)") from the event log, so there's no `resume`.

## Considered options

- One verb per transition (Jira, `gh close`/`reopen`): 9 status verbs, and each tends to accrete its own side effects. Supersedes the "status changes are verbs" note in the CLI draft.
- `update --status`: works, but gives transitions no entry point or help page of their own.
- Storing `waiting` as a status: every blocker, link, hold, child, retire, and sync change would cascade status rewrites (and events) through descendants, and machines could disagree about a derived value. That's the cache rejected in [ADR-0007](0007-readiness-is-one-sql-view.md), made worse.
