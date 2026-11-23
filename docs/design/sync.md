# Sync

Status: agreed 2026-11-20 (loop 2 design session). The spec for `tess sync` and `tess doctor`, written against the core as built. Settled earlier and not repeated: the transport is a ref no branch checks out ([ADR-0004](../adr/0004-sync-over-unchecked-out-git-ref.md)); its tree is one file per issue, comment, event and text ([ADR-0011](../adr/0011-sync-ref-layout.md)); sync runs only when asked, and the local ref moves by compare-and-swap ([ADR-0012](../adr/0012-explicit-sync.md)); events are the field-level log that merging reads ([ADR-0013](../adr/0013-field-level-event-log.md)). New decisions: [ADR-0021](../adr/0021-latest-event-wins.md) (conflict rule), [ADR-0022](../adr/0022-merge-repairs-structure.md) (structure after a merge), [ADR-0023](../adr/0023-id-collisions-refuse.md) (ID collisions). Terms: [GLOSSARY.md](../../GLOSSARY.md), "Sharing".

## What a sync is

A copy's issues live in its database. A sync exchanges them with one remote through `refs/tess/sync`:

1. **Fetch** the remote's ref into a fetched ref the transport keeps (`refs/tess/remotes/<remote>/sync`).
2. **Merge** three snapshots: the LKG (the local `refs/tess/sync`), ours (the database serialised now), theirs (the fetched ref). Absent LKG or absent theirs is an empty snapshot.
3. **Push** the merged snapshot as a commit whose parents are the LKG and theirs, with `--force-with-lease` on the remote ref at the value fetched. A non-fast-forward rejection means the remote moved: fetch, merge, push again, three times at most; then fail with `push_rejected`.
4. **Write** the merged snapshot into the database, in one transaction.
5. **Move** the local ref to the pushed commit, compare-and-swap against the LKG.

The order of 4 and 5 matters. If the database write fails after the push, the local ref has not moved, and the next sync merges the same inputs again and reaches the same result. If the ref moved first and the database write failed, our next snapshot would look like it had reverted everything the remote sent. Nothing is written locally before the push succeeds.

Two syncs on one machine (two worktrees' session-end hooks, say) are serialised by a lock file next to the database; the holder writes its process ID and start time into it, and the second waits up to 30 seconds, then fails with `busy`, naming the holder and how long it has run. `tess doctor` reports a running sync the same way, and warns when one has held the lock for five minutes.

`--dry-run` does steps 1 and 2 and reports what 3 would push, writing nothing but the fetched ref.

## The snapshot

The tree of ADR-0011, with each file's contents fixed here so that an unchanged record produces the same blob on every copy. Every JSON file is one object, keys in the order below, no insignificant whitespace, one trailing newline. Timestamps are RFC 3339 UTC to the second. Absent optional values are `null`.

```
config.json                        {"format":1,"prefix":"tess"}
issues/<id>.json                   one issue, see below
comments/<id>/<comment-id>.json    {"id","issue_id","author","text","created_at"}
events/<id>/<event-id>.json        {"id","issue_id","changed_at","changed_by","attr","old_value","new_value"}
texts/<hash>                       the text's bytes, verbatim, no newline added
```

An issue file holds the stored state, not the displayed one: `status` is the stored status (`ready`, never `waiting`), and there is no `blocked`, `ancestors`, `links`, `comments` or `history`. Text fields hold the hash of their body, as the database does; the body is in `texts/`.

```json
{"id":"tess-a3f","title":"...","description":"<hash>","design":"<hash>","acceptance_criteria":"<hash>","notes":"<hash>",
 "type":"task","priority":2,"status":"ready","status_reason":null,"assignee":null,"owner":null,
 "labels":["a","b"],"parent":null,"blocked_by":[],"related":[],"discovered_from":[],
 "due_at":null,"created_at":"...","created_by":"tester","updated_at":"...","retired_at":null}
```

Each link appears once, on the issue whose event log records it (acceptance README, "Event attribute names"): `blocked_by` on the dependent, `parent` on the child, `discovered_from` on the issue that was discovered. `related` is symmetric and appears on the issue with the smaller ID, so two copies that add the same link in opposite wordings produce the same file. Collections are sorted.

`config.json` carries only the prefix in loop 2. Any later setting decides for itself whether it travels; config keys under `sync.` (last sync time and result) are per copy and never travel. A `format` the binary does not know fails the sync with `format_unsupported` and a hint to upgrade.

## The merge

The merge is a pure function of three snapshots, symmetric in ours and theirs, so every copy that merges the same pair gets the same result. It works issue by issue over every ID present in any of the three.

**Which issues.** New on one side (absent from the LKG and the other side): keep it. Present on both sides but absent from the LKG: the same issue if `created_at` and `created_by` agree (two copies imported the same file, or a copy lost its LKG), merged with an empty LKG record; otherwise two different issues that hash to one ID, and the sync fails with `conflict` naming the ID and both titles ([ADR-0023](../adr/0023-id-collisions-refuse.md)), unless exactly one of the two is retired, in which case the live one is taken whole and the retired record, with its events and comments, is dropped; both retired, the earlier-created one is kept. Present in the LKG and missing from a side is treated as unchanged on that side.

**Which fields.** Each of these is one unit for merging: `title`, `description`, `design`, `acceptance_criteria`, `notes`, `type`, `priority`, `status` together with `status_reason`, `assignee`, `owner`, `due_at`, `parent`, `retired_at`. `labels`, `blocked_by`, `related` and `discovered_from` are collections, and each element is its own unit. `created_at` and `created_by` never change. `updated_at` is the later of the two sides.

**The rule for a unit** ([ADR-0021](../adr/0021-latest-event-wins.md)). Equal on both sides: keep it. Otherwise the side with the later new event wins. A side's new events for a unit are the events in its snapshot that the LKG does not have, whose `attr` names the unit: `status` or `status_reason` for the status unit; for an element of a collection, the events whose `attr` is the collection and whose old or new value is that element, on either endpoint for `related`. The latest of ours against the latest of theirs, by `changed_at` then by event ID. A side with no new event counts as time zero, so the ordinary case, one side edited and the other did not, takes the edit. When neither side has a new event (imported issues, or a snapshot written by hand): the side that differs from the LKG wins if only one does; else the side whose issue `updated_at` is later; still equal, the smaller canonical JSON value. The last two tie-breaks are arbitrary but the same on every copy.

The rule does not shortcut on "equal to the LKG". A copy that changes a title and changes it back has the latest event and wins over the other copy's earlier edit, which is what the latest event means; a plain three-way merge would take the other side. The invariant the simulator checks follows directly: every unit ends with the value its latest event anywhere gave it, unless a retire or a structure rule dropped that value.

A resolution is reported when both sides have a new event for the unit, which is a conflict in the glossary's sense (both changed it since the LKG). Taking the only side that has one is not reported; it is just the edit arriving.

**After a retire.** A copy refuses edits to a retired issue, and the merge does the same for edits the other copy made without knowing. Once `retired_at` has resolved to a retire, every unit the non-retiring side won whose deciding event is later than the retire event is dropped in favour of the retiring side's value, and reported with reason `retired`. Edits earlier than the retire stand. Comments stay, since they are records, not edits. A later restore (`retired_at` back to `null`, with a later event) wins over the retire like any unit and the dropping does not apply.

**Structure** ([ADR-0022](../adr/0022-merge-repairs-structure.md)). Two clean merges can combine into what a single copy would refuse: a cycle through `blocks` and `parent` links, or a `blocks` link between an issue and its own ancestor or descendant. After every issue is merged, the merged snapshot is checked against the rules of [ADR-0016](../adr/0016-hierarchy-rules.md), over all issues including retired ones, as the store's cycle check does. For a violation, the links that take part in it and are new since the LKG are the candidates; the candidate with the latest event is dropped and reported with reason `cycle`, and the check runs again until the snapshot is clean. A second parent cannot arise, since `parent` is a scalar unit.

**Events and comments** are the union of the three snapshots. IDs stay as minted, so history sorts by time across copies. The merge adds no events: a resolution is reported, not recorded ([ADR-0021](../adr/0021-latest-event-wins.md)), and the losing edit's own event is already in the union, so `show --history` shows both edits. A dropped link leaves an `add` event with no matching removal in the history; the issue row is the source of truth, not the log (ADR-0013).

**Config.** Prefixes that differ fail the sync with `prefix_mismatch`: two prefixes means two trackers.

## Writing the merged snapshot

The merged snapshot replaces the database's state for every issue it changed: the issue row, its labels and links, new comments, new events, new texts. The store's ordinary writes log events by diffing patches; the sync write does not, because the events arrive with the snapshot. Rows the merged snapshot does not mention are left alone. The write runs in one `BEGIN IMMEDIATE` transaction, so a command running at the same time sees the old state or the new one.

After the write, `config` gets `sync.last_at` (the sync's time) and `sync.last_result` (`ok`, or the error code).

## Git

Everything git does is done by the user's `git` binary, so authentication, transports and credential helpers are whatever the user already has (ADR-0020, amended). The transport keeps to plumbing: `fetch` with an explicit refspec, `cat-file --batch` and `ls-tree -r` to read a snapshot, `fast-import` or `hash-object`/`mktree`/`commit-tree` to write one, `update-ref` with the expected old value, and `push --force-with-lease`. One git process per step, never one per object: a snapshot of ten thousand issues is written in one `fast-import` stream, and only blobs the LKG does not already have are sent. Blob hashes are computed in-process (SHA-1 over `blob <size>\0` and the bytes) so that "what changed since the LKG" is a tree comparison without a git call per file.

The commit's author and committer are the actor, its time is the clock (so `TESS_NOW` fixes it), and its message is `tess sync` plus the summary line. `git log refs/tess/sync -- issues/<id>.json` is one issue's history of syncs.

The remote is `--remote NAME`, else `origin`, else the only remote; several remotes and no `origin` is `no_remote` with a hint to name one. The remote name is never stored.

## Command surface

```
tess sync [--remote NAME] [--dry-run]
tess doctor [--remote]
```

Both appear in the orientation. Partial output and codes are in the acceptance README; the shape:

- `tess sync` plain output: one line per resolution on stderr, then one summary line on stdout. `resolved tess-a3f status: kept done (this copy, 2026-11-20T10:02:11Z), dropped in-progress (other copy, 2026-11-20T10:01:40Z)`; `dropped tess-a3f title: issue was retired at ...`; `dropped tess-9f2 blocked_by tess-a3f: would make a dependency loop`. Summary: `fetched 12 issues, pushed 3, resolved 1`, or `up to date`, or with `--dry-run`, `would push 3`. `--json` prints the result object on stdout and nothing on stderr.
- Codes: `no_remote`, `offline` (the fetch or push could not reach the remote), `push_rejected` (a policy rejection, or three non-fast-forward rejections in a row), `conflict` (an ID collision), `prefix_mismatch`, `format_unsupported`, `busy`, `not_initialized`. A failed sync changes nothing in the database but `sync.last_result`, which records the code (so the hook and `doctor` can say why), and never moves the local ref; `sync.last_at` stays the time of the last sync that worked. `busy` is not recorded: waiting on another sync is not a sync that failed, and the one that held the lock records its own result.
- `tess doctor`: one line per check, `ok`, `info`, `warn` or `fail`, aligned, with a `fix:` line under a warn or fail. `info` is a fact that is not a problem: no remote, never synced, hooks not set up. Exit 1 only on a `fail`. Checks, in order: `git` (found, version), `repository` (inside one, its common dir), `database` (present, schema version, issue count), `prefix`, `remote` (configured; with `--remote`, reachable, which is the only check that uses the network), `sync` (local ref present, last sync time and result, issues changed since), `hooks` (the Claude hooks of `tess setup claude` present in the repo's `.claude/settings.json`; `tess` on `PATH` when they are), `other trackers` (another tracker's hooks on the same Claude events in the repo's or the user's Claude settings, or a `.beads/` directory in the repo: `warn`, naming what was found and touching nothing). Doctor reads those two settings files and the repository root, and nothing else outside the repository.

## Hooks

`tess hook session-start` runs the sync in-process with a ten-second deadline before printing its facts, unless the hook's `source` is `compact`. `no_remote`, `offline`, `busy` and the deadline are facts, not failures: the `Last sync` line says `just now`, `2 hours ago, 3 issues changed since`, `never`, `not synced (offline)`, or, for a copy that synced before, `3 days ago, not synced now (offline)`. The deadline reads `not synced (timed out)`. The hook never exits non-zero and never prints a resolution. `tess hook session-end` starts `tess sync` detached, with its output discarded, and exits at once.

No ordinary command mentions sync state. "Issues changed since the last sync" appears in `doctor` and the session-start facts, nowhere else.

## Not in loop 2

- The cloud inbox for sessions whose git proxy cannot push a ref (ADR-0004): filed as an issue, revisited when a cloud session can run `tess`.
- The public-remote warning of ADR-0012: needs a definition of "public" that does not need the network; filed as an issue.
- Several remotes in one sync, sync to a remote without `refs/tess/sync` write access, and a `tess sync --from PATH` for air-gapped transfer.
