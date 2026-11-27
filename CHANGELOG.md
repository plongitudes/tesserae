# Changelog

Notable changes to tesserae, newest first. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and versions follow [Semantic Versioning](https://semver.org/), with the usual 0.x caveat that a minor bump may break something. Each release is an annotated git tag carrying the same notes.

## [Unreleased]

The first cut of tess: the tracker, the command line, the Claude Code integration, and sync between copies. It was built in two autonomous agent loops against a locked acceptance suite ([docs/loop-pattern.md](docs/loop-pattern.md)); the design it was built to is in `docs/`.

### Added

- **The tracker.** Issues with a title, type, priority, status, assignee, owner, labels, due date, and four texts (description, design, acceptance, notes); comments; and a field-level history of every change. Four link types: `blocks`, `parent`, `related`, `discovered-from`. One definition of blocked, derived from `blocks` links and held or blocked ancestors; an issue with open children is not ready and cannot be finished. Links that would make a dependency loop, or a `blocks` into an issue's own hierarchy, are refused. Issues are retired rather than deleted, and can be restored.
- **The database.** One SQLite file in git's common directory, `tess.db`, shared by every worktree. IDs are a prefix and a base36 hash that grows as the tracker fills.
- **The command line.** `init`, `create`, `show` (`--tree`, `--history`, `--field`), `update` (`--append`), `status`, `comment`, `link`, `unlink`, `ready`, `list`, `retire`, `restore`, `import`, `sync`, `doctor`, `setup`, `hook`, `version`. Every command takes `--json` and prints stable shapes; errors are an object with a code and a hint. Help reads as man-page syntax and every command has examples. `tess` alone prints a one-screen orientation.
- **Import from beads.** `tess import` reads a beads `issues.jsonl`, keeping IDs and timestamps and mapping statuses and link types.
- **Claude Code integration.** `tess setup claude` writes a skill and two hooks. The session-start hook syncs with a deadline and prints facts, never instructions; the session-end hook starts a sync in the background. Changes made in a session are recorded as `claude:<session>`.
- **Sync.** `tess sync` exchanges issues through `refs/tess/sync`, a ref no branch checks out, with any git remote. Three-way merge by field, the later change winning; every change kept in the history; structure repaired after a merge and the repair reported; ID collisions refused with both issues named. Nothing is written locally before the push succeeds. Syncs on one machine are serialised by a lock that names its holder. Failures are codes: `no_remote`, `auth_failed`, `host_unresolved`, `connection_refused`, `connection_timed_out`, `network_unreachable`, `remote_not_found`, `offline`, `push_rejected`, `conflict`, `prefix_mismatch`, `format_unsupported`, `busy`.
- **Doctor.** `tess doctor` checks git, the repository, the database, the prefix, the remote (`--remote` reaches it), the sync state including a running sync, the hooks and which binary they run, and other trackers' leftovers.
- **Version.** `tess version` says which build this is: the installed version, or the commit of the checkout it was built from.
- **A fresh clone needs no init.** `tess sync` on a repository with no database creates it from the remote's snapshot, prefix included, and the session-start hook does the same; `tess import` on one creates it with `--prefix` or the prefix the imported IDs share. A database that is present but cannot be opened is never replaced.

### Development

- The gate, `scripts/ralph/check.sh --strict`: gofmt, vet, staticcheck, unit tests with nothing skipped, a build, the fifteen acceptance scripts, and the simulator's scenarios and random runs. CI runs it on Linux and macOS and builds on Windows.
