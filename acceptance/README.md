# Acceptance suite

The behavioural contract for loop 1 and, from the section "Loop 2" on, loop 2 ([build plan](../docs/design/build-plan.md)). Every script in `testdata/` must pass. **This directory is locked: the build loop must never edit, delete, or skip anything under `acceptance/`.** If a script looks wrong, stop and ask a human. The loop may write as many tests of its own as it likes elsewhere.

Run with `go test ./acceptance/...`: the scripts in `testdata/`, and the loop-2 simulator in `sim/`.

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
- **Orientation.** `tess` with no arguments prints one line per working command (`ready`, `create`, `show`, `update`, `status`, `comment`, `link`, `unlink`, `list`, `retire`, `restore`, `sync`, and from loop 2 `doctor`) and names none of `init`, `import`, `setup`, `hook`.
- **Dates.** `--due` accepts `YYYY-MM-DD` (UTC midnight) or RFC 3339. Output is always RFC 3339 UTC.
- **Filters.** `list --status` matches the displayed status, so `--status waiting` works even though `waiting` can't be set.
- **Import reasons.** bd `blocked` → `held` with reason `imported from bd status blocked`; bd `deferred` → `held` with reason `deferred until <YYYY-MM-DD>` when `defer_until` is set (else `imported from bd status deferred`); bd `closed` → `done` with its `close_reason`; bd `tombstone` → retired, with `retired_at` from `deleted_at` (else `updated_at`). Re-importing skips IDs that already exist and never overwrites. An imported issue's history holds one synthetic `status` event when bd recorded a change of state (added 2026-11-17): `ready → done` at bd's `closed_at` (else `updated_at`) by `closed_by` (else the importer's actor) for a `closed` issue; `ready → held` at `updated_at` by the importer's actor for `blocked`, `deferred`, and any status mapped to `held`. `open`, `in_progress`, and tombstones get no event. `ready` as the old value is a guess, since bd kept no earlier status; it is the right one for most issues and harmless where it is wrong.

- **Import keeps original IDs.** `bd-abc` stays `bd-abc`, including old dotted child IDs like `bd-abc.1`, so references in old commits still resolve. New issues use the repo's prefix. Import is all-or-nothing: on any error (including a cycle) nothing is imported. Old statuses map per [ADR-0009](../docs/adr/0009-status-model.md); old `tombstone` → retired; `parent-child` → `parent`; `blocks`, `related`, `discovered-from` carry over; `duplicates`, `supersedes`, and any other type → `related`.
- **`tess sync` in loop 1** exited 0 and printed `sync: not available yet` to stderr. Loop 2 replaces it (below).
- **Retired issues** never match a partial ID, so `show` needs the exact ID. They're excluded from `list` even with `--all`; `list --retired` lists only them.
- **ID shape.** `<prefix>-<base36>`, where base36 is lowercase `[0-9a-z]`, at least 3 characters, and never contains a dot for new issues ([ADR-0017](../docs/adr/0017-flat-immutable-ids.md)).

## Loop 2: sync and doctor

The contract for [sync.md](../docs/design/sync.md). Scripts: `sync.txtar` (two copies through a bare remote, in the work directory), `doctor.txtar`, and changes to `cli.txtar` and `init.txtar`. The simulator in `sim/` is the rest of the contract and is locked like everything here.

### What the binary must provide

- `tess sync [--remote NAME] [--dry-run]` and `tess doctor [--remote]`, both in the orientation.
- A copy is a git repository with its own common dir, so two `git init` directories with the same remote are two copies. The remote is any git remote; the scripts use a bare repository on disk. Nothing in the scripts names the ref the transport fetches into.
- `TESS_NOW` fixes every timestamp sync writes, including the commit's, and is the time the conflict rule compares, so a script orders edits across copies by setting it.
- Errors are the usual envelope. Codes: `no_remote`; for a remote that gave no answer, by git's reason, `host_unresolved`, `connection_refused`, `connection_timed_out`, `network_unreachable`, `auth_failed` (any credential or key problem; the message says no more than git does), `remote_not_found` (the URL is not a repository), and `offline` for a reason git words some other way; `push_rejected`, `conflict`, `prefix_mismatch`, `format_unsupported`, `busy`; and `not_initialized` as everywhere. After a failed sync nothing has changed: the database, and `refs/tess/sync` in the copy's repository, are as before.

### Sync output (suite decision)

Plain mode: one line per resolution on stderr, then one summary line on stdout.

```
resolved t-a3f title: kept "From b" (other copy, 2026-01-03T00:00:00Z), dropped "From a" (this copy, 2026-01-02T00:00:00Z)
dropped t-a3f title: issue was retired at 2026-01-05T00:00:00Z
dropped t-9f2 blocked_by t-a3f: would make a dependency loop
fetched 2 issues, pushed 1, resolved 1
```

The summary is `fetched N issues, pushed N, resolved N` (`issue` when N is 1; `resolved` only when non-zero), `up to date` when nothing moved either way, and `would push N` (plus `would fetch N`) with `--dry-run`. `fetched` counts issues the merge changed from this copy's point of view (new or changed from the remote); `pushed` counts issues changed from the remote's (new or changed here). With `--json`, stdout is:

```json
{"remote": "origin", "fetched": 2, "pushed": 1, "dry_run": false,
 "resolved": [ {"issue": "t-a3f", "field": "title", "kept": "From b", "dropped": "From a", "won": "remote", "reason": "later",
                "kept_at": "2026-01-03T00:00:00Z", "dropped_at": "2026-01-02T00:00:00Z"} ]}
```

`field` is a unit of the merge: a scalar field name, `status` (the status and its reason together; `kept`/`dropped` are the status values), or a collection name (`labels`, `blocked_by`, `related`, `discovered_from`) with the element as `kept`/`dropped`. `won` is `local` or `remote`. `reason` is `later` (the later event won), `retired` (dropped because the issue had been retired first; `kept` is the retiring side's value), or `cycle` (a link dropped to keep the hierarchy rules; `field` is `blocked_by` or `parent`, `dropped` the other issue's ID, `kept` null). `kept_at` and `dropped_at` are the times of the events that decided the unit on each side, as the plain output prints them: for `later`, each side's latest new event; for `retired`, `kept_at` is the retire's time; for `cycle`, `dropped_at` is the dropped link's event. Null when that side had no event. `resolved` is always an array.

`--dry-run` fetches, merges and reports; it pushes nothing, writes nothing to the database, and leaves `refs/tess/sync` where it was.

### Doctor output (suite decision)

Plain mode, one line per check: the level, the check's name, and a detail, aligned; a `fix:` line under any `warn` or `fail`.

```
ok    git           git 2.56.0
ok    repository    /path/.git
ok    database      /path/.git/tess.db, 12 issues
ok    prefix        t
info  remote        no remote configured
      fix: git remote add origin URL, then tess sync
info  sync          never synced
info  hooks         not set up; tess setup claude installs them
ok    other_trackers  none found
```

With `--json`, stdout is an array of `{"check", "level", "detail", "fix"}` in that order, `fix` being `null` when there is none. Checks: `git`, `repository`, `database`, `prefix`, `remote`, `sync`, `hooks`, `other_trackers`. Exit 1 only when some level is `fail`; `warn` and `info` exit 0. Outside a git repository, `repository` fails and the checks after it are skipped (absent from the output). Without a database, `database` fails with the fix `tess init`. `remote` is `info` when none is configured and `ok` when one is; with `--remote` it asks the remote for the sync ref (one `ls-remote`, nothing fetched), and an unreachable remote is `warn`, not `fail`. `repository` names a repository whose objects are sha256 in its detail. `sync` is `info` before any sync, `ok` with the last sync's time and the issues changed since, and `warn` when the last try failed with a code (other than `no_remote`) or the local snapshot ref is gone; when another sync holds the lock, the detail adds `a sync is running now (pid N, for D)`, and after five minutes that becomes a `warn` naming the process. `hooks` is `info` when `tess setup claude` has not been run, `ok` when its hooks are in the repo's `.claude/settings.json` and the `tess` that `PATH` finds is the binary running, and `warn` when the hooks are there but `tess` is not on `PATH` (the fix names the running binary's directory) or `PATH` finds a different `tess` (the detail names both). `other_trackers` is `warn` when another tracker's hooks sit on `SessionStart` or `SessionEnd` in the repo's Claude settings (`.claude/settings.json` or `.claude/settings.local.json`) or the user's (`$CLAUDE_CONFIG_DIR/settings.json` when that is set, else `~/.claude/settings.json`), any hook command naming `bd` or `beads`, or the repository has a `.beads/` directory; the detail names what was found and nothing is changed. Doctor reads only those three settings files, the repository root, and `PATH` for `tess`.

### Hooks

`tess hook session-start` runs the sync first (ten-second deadline, unless `source` is `compact`) and its `Last sync` fact becomes `Last sync: just now` after a success, `Last sync: never` when the copy has never synced and the sync could not run (no remote), and `Last sync: not synced (<code>)` when the sync failed for another reason; a copy that synced before and fails now says `Last sync: <when>, not synced now (<code>)`. When the ten-second deadline ended the sync, the code reads `timed out; run tess sync to finish any login`, and `auth_failed` reads `auth_failed; run tess sync to log in`, since a credential prompt may have been waiting on the user. The hook runs git with its terminal prompt off. "Just now" is under a minute by the clock. The hook still exits 0 and prints nothing to stderr.

### The simulator (`sim/`)

Property tests and red-team scenarios over two or three copies in temp directories, syncing through a bare repository, all in-process, no network. They use exactly this Go API, which `internal/sync` must export (package name `sync`):

```go
type Snapshot struct {
    Config   Config
    Issues   map[string]*Issue     // by issue ID
    Comments map[string]*Comment   // by comment ID
    Events   map[string]*Event     // by event ID
    Texts    map[string]string     // by hash
}
type Config struct { Format int; Prefix string }
type Issue struct {
    ID, Title, Description, Design, AcceptanceCriteria, Notes, Type string   // texts are hashes
    Priority int
    Status string                              // the stored status, never waiting
    StatusReason, Assignee, Owner *string
    Labels []string
    Parent *string
    BlockedBy, Related, DiscoveredFrom []string   // Related: on the smaller ID only
    DueAt *time.Time
    CreatedAt time.Time
    CreatedBy string
    UpdatedAt time.Time
    RetiredAt *time.Time
}
type Comment struct { ID, IssueID, Author, Text string; CreatedAt time.Time }
type Event struct { ID, IssueID string; ChangedAt time.Time; ChangedBy, Attr string; OldValue, NewValue any }
type Resolution struct { Issue, Field string; Kept, Dropped any; Won, Reason string; KeptAt, DroppedAt time.Time }   // as in the JSON above; zero time = null
type Options struct { Dir, DBPath, Remote string; DryRun bool; Now func() time.Time; Actor string; NoPrompt bool }   // NoPrompt: git asks for no password on the terminal
type Result struct { Remote string; Fetched, Pushed int; Resolved []Resolution; DryRun bool }

func (s *Snapshot) Files() map[string][]byte                       // the canonical tree, path -> bytes
func Merge(lkg, ours, theirs *Snapshot) (*Snapshot, []Resolution, error)   // pure; nil lkg or theirs is empty
func Load(ctx context.Context, dbPath string) (*Snapshot, error)   // the database as a snapshot
func Run(ctx context.Context, o Options) (*Result, error)          // the whole sync; errors are *tracker.Error
```

`Resolution.Kept`/`Dropped` hold the JSON values (`string`, `float64`, `nil`). `Files()` must give byte-identical output for equal snapshots, whatever copy produced them. The simulator drives copies through `tracker.Init`, `tracker.Open` and the `Tracker` interface, one shared clock that advances one second per call so no two events share a time, and one actor name per copy. What it checks after every run, with every copy synced twice at the end: all copies' `Files()` are identical; every event and comment any copy made is in every copy; every unit of every issue holds the value of its latest event anywhere, except a unit a resolution with reason `retired` or `cycle` named; and the hierarchy rules hold on the result. A run in which two copies' new issues collide on an ID is expected to fail with `conflict` and is counted, not failed.

`TESS_SIM_RUNS` sets how many random runs (default 25; `-short` runs 5). A failure prints the seed and the operation log; `TESS_SIM_SEED` replays one seed.
