---
status: accepted
date: 2026-11-04
---

# Status model: five stored statuses, two derived

An issue shows one of seven statuses: `waiting`, `ready`, `in-progress`, `pending-review`, `held`, `done`, `omit` (vocabulary borrowed from ShotGrid's task statuses). Only five are stored and set by people: not started (set as `ready`; [ADR-0018](0018-one-status-command.md)), `in-progress`, `pending-review`, `held`, and the terminal `done` and `omit`. A not-started issue displays as `waiting` when it's blocked and `ready` when it isn't. The distinction is derived from the readiness view ([ADR-0007](0007-readiness-is-one-sql-view.md)) and can't be set by hand, so status and dependencies can never disagree.

## Considered options

- Storing `waiting`/`ready` like ShotGrid does: ShotGrid can't compute dependencies, so people set them. We can, and storing them would bring back the old tool's disagreeing definitions of "blocked".
- A separate `stuck` status for external blockers: dropped. External waits are `held` with a reason, or stay `in-progress` if work continues around them.
- Separate `deferred` and `held` statuses, plus a `defer_until` field: merged into `held`, with no until-date. A snooze date would be the only place time changes state on its own, and it would need a second mechanism for restoring the pre-hold status. The reason field says what a hold is waiting on, and `tess list --status held` sorted by hold age surfaces stale holds. A snooze can be added later without touching anything else.

## Consequences

- One `status_reason` field replaces `CloseReason` and serves `held` and `omit` too.
- `done` and `omit` are separate outcomes, so "what got done" is a clean query.
- Holding an issue blocks its descendants (through the same readiness view), so holding an epic pauses its work; children keep their stored status and come back when the hold ends.
- A hold ends only when a person sets another status. `show` displays the status from before the hold, read from the event log ([ADR-0013](0013-field-level-event-log.md)); `done` and `omit` issues can't be held.
- `done` and `omit` can be brought back to not started or straight to in-progress.
- Omitting a blocker unblocks its dependents, the same as `done`; setting `omit` lists them as newly unblocked so someone can check whether they still make sense.
- An `in-progress` issue can become blocked if someone adds a blocker mid-work; it keeps its status and shows a blocked marker.
- Import from `bd`: `open` → not started, `in_progress` → `in-progress`, `blocked`/`deferred` → `held` (a future `defer_until` becomes the reason "deferred until <date>"), `closed` → `done`.
