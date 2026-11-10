# Requirements

What the tool must do and for whom. Decisions about how live in [adr/](adr/); module structure in [design/](design/).

## Users

A solo developer on several machines (including remote/cloud Claude sessions), using git worktrees with parallel agents. Small-team use may come later.

## Data model

Issue fields:

- Core: `ID`, `Title`, `Description`, `Status`, `Priority` (0–4, 0 highest), `IssueType`, `Assignee`, `CreatedAt`, `UpdatedAt`, `ClosedAt`, `Labels`, `Dependencies`, `Comments`.
- Extended: `Design`, `AcceptanceCriteria`, `Notes`, `EstimatedMinutes`, `CreatedBy`, `Owner`, `StatusReason` (why held, omitted, or done), `DueAt`.
- `RetiredAt`: set when an issue is retired ([ADR-0010](adr/0010-retire-instead-of-delete.md)); null means live.
- Probably: `ExternalRef` (an issue's identity in a remote tracker; unique).

Statuses ([ADR-0009](adr/0009-status-model.md), [ADR-0018](adr/0018-one-status-command.md)): `ready` (not started; shows as `waiting` when blocked), `in-progress`, `pending-review`, `held`, `done`, `omit`.

Link types ([ADR-0015](adr/0015-four-link-types.md)): `blocks`, `parent`, `related`, `discovered-from`. Only `blocks` and `parent` affect readiness or participate in cycle detection. An issue has at most one parent.

Comments: id, issue id, author, text, created at.

## Behaviour

- The binary is `tess`, with no alias (`ts` collides with moreutils and TypeScript shorthand).

- An issue is **blocked** when it has a `blocks` dependency on an issue that isn't `done` or `omit`, or when any ancestor (via `parent`) is blocked or `held`.
- An issue with open children isn't ready and can't be marked done or omitted until they finish ([ADR-0016](adr/0016-hierarchy-rules.md)).
- A not-started issue displays as **waiting** when blocked and **ready** when not. A `held` issue stays held until someone sets another status; `show` displays what it was before.
- IDs are `<prefix>-<base36 hash>`, growing in length with issue count, flat for every issue including children, and never change ([ADR-0017](adr/0017-flat-immutable-ids.md)).
- Partial IDs resolve by exact match, then prefix, then unique substring, with an error listing candidates when ambiguous.
- Capabilities (the old `bd` commands, as a checklist, not a CLI spec): create, update, show, list, ready, blocked, close, reopen, dependency add/remove/tree/cycles, labels, comments, search, stats, defer. Every read command has machine-readable JSON output. The CLI surface itself is designed fresh ([ADR-0008](adr/0008-cli-compatible-in-spirit.md)).
- Output uses small Unicode status symbols (`○ ◐ ● ✓ ❄`) with semantic colours, no emoji.
- Imports the old `.beads/issues.jsonl` so existing issues come along.
- Works offline; works identically from every worktree of a repo.
- Every change is recorded: which field, old and new value, who, and when.

## Acceptance

The old repo's `cmd/bd/testdata/*.txt` testscripts (create, ready, blocked, close, dep, list, show, update, import, export, stats, init, help, quickstart, version) are the behavioural spec for the first spike.

## CLI

See [design/cli.md](design/cli.md).
