---
status: accepted
date: 2026-11-09
---

# Every change is recorded as a field-level event

Modelled on ShotGrid's EventLog. Each change to an issue appends one event, and there is a single schema with no event types:

```json
{ "id": "<ULID>", "issue_id": "tess-a3f", "changed_at": "<UTC timestamp>", "changed_by": "<actor>",
  "attr": "status", "old_value": "in-progress", "new_value": "done" }
```

Collections log one element per event with `null` on the absent side: a label added is `labels: null → "auth"`, a link removed is `blocked_by: "tess-9f2" → null`. Links are recorded on the issue holding them: `blocked_by` on the dependent, `parent` (a scalar) on the child. Values are JSON, so numbers stay numbers. Large text fields log content hashes, not bodies ([ADR-0014](0014-content-addressed-text.md)).

## Rules

- Only the store writes events, inside the same transaction as the change, by diffing the typed patch. No caller can forget.
- The issue row is the source of truth for queries; events sit alongside it and are never replayed to build state (this is not event sourcing).
- Creation logs nothing: `created_at`/`created_by` on the issue are the record. Comments are their own records, not events, and are immutable in v1.
- `changed_by` is `TESS_ACTOR` if set to a non-empty value; else `claude:<session8>` when running under Claude Code ([ADR-0019](0019-agent-context-integration.md)); else git's `user.name`.
- Events sync as one append-only file each (`events/<issue>/<event-id>.json`), so they never conflict.

## Consequences

- `held` is a stored status; resuming restores the status from before the hold, read from the latest `status` event. No pre-hold field, no held flag.
- Hold age, review age, and "status before" all come from events; there's no `StatusChangedAt` field.
- Sync can merge conflicting edits field by field using event times instead of the issue's `updated_at`.
- `tess show --history` shows the log, interleaved with comments.
- Supersedes the earlier decision to drop the events table: the old tool's table failed because nothing read it, and this one has three readers (resume, merge, history). The sync ref's commit history isn't an audit trail: it only sees state at sync time and can't say who.
