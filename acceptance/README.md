# Loop 1 acceptance suite

The behavioural contract for loop 1 ([build plan](../docs/design/build-plan.md)). Every script in `testdata/` must pass. **This directory is locked: the build loop must never edit, delete, or skip anything under `acceptance/`.** If a script looks wrong, stop and ask a human. The loop may write as many tests of its own as it likes elsewhere.

Run with `go test ./acceptance/...`.

Where this file and the design docs disagree, the design docs win and this suite gets fixed by a human. Items marked **(suite decision)** were settled while writing the suite, not in an ADR. They're up for review.

## What the binary must provide

- **Entry point.** Package `github.com/plongitudes/tesserae/internal/cli` exports `func Main() int`, which runs the CLI using `os.Args`, `os.Stdin`, `os.Stdout`, `os.Stderr` and the environment, and returns the exit code. `cmd/tess/main.go` is just `os.Exit(cli.Main())`. The harness runs `Main` in-process through testscript.
- **Clock seam (suite decision).** If `TESS_NOW` is set (RFC 3339, UTC), it's the current time for every timestamp the process writes. The harness sets it to `2026-01-01T00:00:00Z`; scripts change it with `env TESS_NOW=...` when order matters.
- **Actor.** `changed_by` and `created_by` come from `TESS_ACTOR` if set and non-empty; else `claude:<first 8 characters of CLAUDE_CODE_SESSION_ID>` if `CLAUDECODE=1`; else git's `user.name` ([ADR-0019](../docs/adr/0019-agent-context-integration.md)). The harness sets `user.name` to `tester` and clears the other variables.
- **Exit status.** 0 on success, 1 on any error.
- **Non-TTY.** Scripts never run on a terminal, so lists are flat by default and there's no colour. Status symbols are still printed; only colour depends on a TTY. A flat line is `<symbol> <id> <title>` (suite decision).
- **stderr with `--json`** carries only the error object, if any. Factual notes are suppressed.

## JSON contract (suite decision)

Every command accepts `--json`. Field names are snake_case. Absent optional values are `null`, and empty collections are `[]`, never missing.

### Issue object

Used by `create`, `update`, `show`, `list`, and `ready`:

```json
{
  "id": "t-3kq",
  "title": "Add token refresh",
  "description": "",
  "design": "",
  "acceptance_criteria": "",
  "notes": "",
  "type": "task",
  "priority": 2,
  "status": "ready",
  "status_reason": null,
  "blocked": false,
  "assignee": null,
  "owner": null,
  "labels": [],
  "parent": null,
  "ancestors": [],
  "due_at": null,
  "created_at": "2026-01-01T00:00:00Z",
  "created_by": "tester",
  "updated_at": "2026-01-01T00:00:00Z",
  "retired_at": null
}
```

- `status` is the displayed status: `ready`, `waiting`, `in-progress`, `pending-review`, `held`, `done`, or `omit`. `blocked` is the computed condition, whatever the status: an active `blocks` blocker, or a blocked or held ancestor. Open children make a `ready` issue display as `waiting` but do not make it `blocked`, and `list --blocked` doesn't include it ([ADR-0016](../docs/adr/0016-hierarchy-rules.md)).
- `labels` are sorted. `ancestors` run from the nearest parent outwards.
- `show` adds `links`, `comments`, and, for a held issue, `previous_status`:

```json
"links": {
  "blocks": [], "blocked_by": [], "children": [],
  "related": [], "discovered_from": [], "discovered": []
},
"comments": [ { "id": "...", "author": "tester", "text": "...", "created_at": "..." } ],
"previous_status": "pending-review"
```

  `links.*` are arrays of issue IDs, sorted, and omit retired issues (the link rows persist and reappear on `restore`). `parent` sits on the issue itself. `related` is symmetric: `A related B` shows up under `related` on both issues. `discovered_from` on B lists A, and `discovered` on A lists B.
- `show --history` adds `history`: the issue's events, oldest first, each `{"id", "changed_at", "changed_by", "attr", "old_value", "new_value"}` ([ADR-0013](../docs/adr/0013-field-level-event-log.md)). Large text fields log content hashes, not bodies ([ADR-0014](../docs/adr/0014-content-addressed-text.md)).
- `show --tree` adds `tree`: `{"id", "title", "status", "children": [...], "blocked_by": [...]}` nested nodes with the same keys, running downwards from the issue. Plain output prints the issue, then the tree indented under it. (Replaces the `tree` command, 2026-11-16.)
- `show --field F` takes one ID and prints only that field. Plain: text fields exactly as stored (no newline added), scalars on one line, `null` as nothing at all. `--json`: the field's value as JSON (`"text"`, `2`, `null`, `["a","b"]`). `F` is any key of the issue object; an unknown key is `invalid_argument`.

### Command results

| Command | `--json` stdout |
|---|---|
| `create`, `update`, `show` | one issue object |
| `list`, `ready` | array of issue objects, in display order |
| `status`, `retire` | `{"updated": [issues], "newly_unblocked": [issues]}` |
| `restore` | one issue object |
| `link`, `unlink` | an array of `{"from": id, "type": t, "to": id}`, one per pair, normalised to the canonical direction (`blocks`: blocker → blocked; `parent`: parent → child; `discovered-from`: discovered → origin). `tess link A B blocks C D` makes every left × right pair (A→C, A→D, B→C, B→D), in that order, in one transaction: any refusal means no link is made. Adding a link that already exists, in either wording, is a no-op success returning the existing link. Plain output is one `linked A blocks C` line per pair |
| `comment` | the comment object |
| `init` | `{"prefix": p, "database": path}`; `database` is absolute |
| `import` | `{"imported": n, "skipped": n}` |
| `setup claude` | `{"written": [paths], "unchanged": [paths]}`; paths are relative to the repo root. Re-running is a successful no-op that changes no file; only `--check` exits 1 when something is missing |

Ordering for `list` and `ready`: priority ascending, then `created_at`, then `id`.

### Errors

stderr is `{"error": {"code": "...", "message": "...", "hint": "..."}}`, exit 1. `hint` is a one-line suggestion and may be `null`. Plain mode prints `error: <message>` and then, for a refusal, `hint: <hint>`. A command-line error (wrong number of arguments, unknown command or flag, bad flag value) prints instead `usage: <the command's use line>` and `run 'tess COMMAND --help' for examples`, and its JSON `hint` is that use line (dogfood checkpoint, 2026-11-16). Stable codes:

| Code | When |
|---|---|
| `not_initialized` | no database for this repo yet |
| `already_initialized` | `init` when one exists |
| `not_a_git_repo` | run outside a git repository |
| `not_found` | no issue matches the ID |
| `ambiguous_id` | a partial ID matches more than one issue; `message` lists them |
| `invalid_argument` | bad flag value, missing title, unknown command or flag, etc. |
| `invalid_status` | unknown or unsettable status (e.g. `waiting`) |
| `invalid_transition` | e.g. `held` on a `done`/`omit` issue |
| `reason_required` | `held`/`omit` without `-r` |
| `blocked` | `done` while blocked, without `--force` |
| `has_open_children` | `done`/`omit` while children are open |
| `cycle` | a `blocks` or `parent` link would close a loop, including a self-link; also a `blocks` link between an issue and its own ancestor or descendant, in either direction ([ADR-0016](../docs/adr/0016-hierarchy-rules.md)) |
| `has_parent` | a second `parent` link |
| `retired` | mutating a retired issue (other than `restore`) |

## Other suite decisions

- **Event attribute names.** Scalars use the field name (`status`, `title`, `priority`, `assignee`, `retired_at`, …; `status` logs stored values, so not-started is `ready`). Labels use `labels`, one element per event. Links are logged once, on the dependent or child side only: `blocked_by` (value: the blocker's ID), `parent` (a scalar; unlinking logs `P → null` and linking logs `null → P`), and `related` and `discovered_from` on the `from` side of the canonical direction. Large text fields log 64-character lowercase hex SHA-256 hashes. Only fields whose value actually changed produce events; `updated_at` never does.
- **`update` flags.** `--title`, `-t/--type`, `-p/--priority`, `-d/--description`, `--design`, `--acceptance`, `--notes`, `-a/--assignee`, `--owner`, `--due`, `--add-label`, `--remove-label`. Text flags take `-` to read stdin, taken verbatim including any trailing newline. An empty value clears an optional field. There is no `--status`.
- **`update --append`.** With `--append`, each of `-d`, `--design`, `--acceptance`, `--notes` given adds to the field instead of replacing it: the old text, then exactly one blank line (the old text's trailing newlines are normalised to one before the separator), then the new text verbatim. An empty field just takes the new text. `--append` with no text flag, or with `--title`, is `invalid_argument`. The event logs the hash of the resulting text, as any text change does.
- **`list -s`.** The substring search also matches the issue `id`, so `tess list -s 7kq` finds `api-7kq`. Case-insensitive.
- **Orientation.** `tess` with no arguments prints one line per working command (`ready`, `create`, `show`, `update`, `status`, `comment`, `link`, `unlink`, `list`, `retire`, `restore`, `sync`) and names none of `init`, `import`, `setup`, `hook`.
- **Dates.** `--due` accepts `YYYY-MM-DD` (UTC midnight) or RFC 3339. Output is always RFC 3339 UTC.
- **Filters.** `list --status` matches the displayed status, so `--status waiting` works even though `waiting` can't be set.
- **Import reasons.** bd `blocked` → `held` with reason `imported from bd status blocked`; bd `deferred` → `held` with reason `deferred until <YYYY-MM-DD>` when `defer_until` is set (else `imported from bd status deferred`); bd `closed` → `done` with its `close_reason`; bd `tombstone` → retired, with `retired_at` from `deleted_at` (else `updated_at`). Re-importing skips IDs that already exist and never overwrites.

- **Import keeps original IDs.** `bd-abc` stays `bd-abc`, including old dotted child IDs like `bd-abc.1`, so references in old commits still resolve. New issues use the repo's prefix. Import is all-or-nothing: on any error (including a cycle) nothing is imported. Old statuses map per [ADR-0009](../docs/adr/0009-status-model.md); old `tombstone` → retired; `parent-child` → `parent`; `blocks`, `related`, `discovered-from` carry over; `duplicates`, `supersedes`, and any other type → `related`.
- **`tess sync` in loop 1** exits 0 and prints `sync: not available yet` to stderr. Loop 2 replaces it.
- **Retired issues** never match a partial ID, so `show` needs the exact ID. They're excluded from `list` even with `--all`; `list --retired` lists only them.
- **ID shape.** `<prefix>-<base36>`, where base36 is lowercase `[0-9a-z]`, at least 3 characters, and never contains a dot for new issues ([ADR-0017](../docs/adr/0017-flat-immutable-ids.md)).
