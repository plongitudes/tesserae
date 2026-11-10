# Operations

What people and agents need to be able to do, independent of CLI verbs. The verbs in [cli.md](cli.md) map onto these; when they disagree, this file wins and the verbs change. Started 2026-11-07.

## Move work through its life

| # | Goal | From → to |
|---|---|---|
| 1 | Capture work | → not started |
| 2 | Begin work | not started → in-progress |
| 3 | Stop work without finishing (abandon, hand back) | in-progress → not started |
| 4 | Ask for review | in-progress → pending-review |
| 5 | Accept the work | pending-review → done |
| 6 | Send back for changes | pending-review → in-progress |
| 7 | Finish without review | not started / in-progress → done |
| 8 | Drop it | any open state → omit |
| 9 | Bring a finished or dropped issue back | done / omit → not started or in-progress |

## Pause and remove

| # | Goal |
|---|---|
| 10 | Pause it with a reason, and un-pause it back to where it was |
| 11 | Remove it as a mistake, and undo that |

## Change who and what

| # | Goal |
|---|---|
| 12 | Set or clear who owns an issue, separate from beginning work |
| 13 | Edit fields (title, description, priority, type, due date, …) |
| 14 | Add or remove labels |
| 15 | Comment |
| 16 | Link or unlink issues |

## Look

| # | Goal |
|---|---|
| 17 | See one issue in full |
| 18 | Find issues: filters, search, the ready queue, blocked issues |
| 19 | See why something is blocked, and the dependency tree around it |

## Housekeeping

Set up a repo, import from `bd`, sync.

## Mapping to commands

| Goals | Command |
|---|---|
| 1 | `create` |
| 2–10 | `status ID STATUS [-r REASON]` ([ADR-0018](../adr/0018-one-status-command.md)) |
| 11 | `retire` / `restore` |
| 12–14 | `update` |
| 15 | `comment` |
| 16 | `link` / `unlink` |
| 17–19 | `show`, `list`, `ready`, `tree` |
| Housekeeping | `init`, `import`, `sync` |

## Out of v1

Split an issue, merge duplicates, move an issue to another repo.

## Precedents

- Jira: assigning is separate from starting; has "Stop Progress" (goal 3); its "flag" marks an issue impeded without changing status.
- GitHub: no review status; `close` takes a reason, "completed" or "not planned" (goals 5/7 vs 8).
- Linear: status categories Backlog, Todo, In Progress, In Review, Done, Canceled map almost 1:1 onto ours.
- beads: status is a settable field (`update --status`), plus `close`/`reopen`.
- ShotGrid: status is a settable field with no verbs, and an EventLog records every field change on every entity (who, what, when, old value, new value).
