---
name: tess
description: Track this repository's work with tess, its issue tracker. Use when the user asks what to work on next, when starting or finishing a task, when you find a bug or follow-up work worth recording, or when issues, blockers, priorities or statuses come up. Covers finding ready issues, creating and linking them, and setting status.
allowed-tools: Bash(tess *)
---

# tess

tess is this repository's issue tracker. The issues are in one database that every worktree shares; nothing is added to the working tree. `tess` alone prints a short orientation, and `tess COMMAND --help` has flags and examples. Add `--json` to any command whose output you will read.

## Work loop

1. Find work: `tess ready --json` lists the issues that can be started now, most urgent first.
2. Read it: `tess show ID --json` has the description, acceptance criteria, links and comments.
3. Claim it: `tess status ID in-progress`.
4. Do the work, and record what you find as you go:
   - new work: `tess create "Title" -t bug -p 1 -d "What and why"`, then `tess link NEW discovered-from ID`
   - a part of this issue: `tess create "Title" --parent ID`
   - work that has to come first: `tess create "Title"`, then `tess link NEW blocks ID`
   - several links at once: `tess link A B blocks C` links every issue on the left to every issue on the right
   - a note as you go: `tess update ID --notes "What I tried" --append` adds a paragraph and keeps what is there
   - a decision or a finding: `tess comment ID "What was decided and why"`
   - stuck on something outside the work: `tess status ID held -r "what it is waiting for"`
5. Finish: `tess status ID done`, or `tess status ID pending-review` when a person should look first.
6. Run `tess sync` when you finish a unit of work, so other machines and sessions see it.

## Statuses

- Settable: `ready`, `in-progress`, `pending-review`, `held`, `done`, `omit`. `held` and `omit` need a reason: `tess status ID held -r "waiting for the API key"`.
- `waiting` is shown, never set: a `ready` issue that is blocked or still has open children.
- `done` is refused with `blocked` while a blocker is open (`--force` overrides), and `done` and `omit` are refused with `has_open_children`.
- Status changes only through `tess status`. `tess update` changes every other field.
- There is no delete. `tess retire ID -r "why"` hides an issue; `tess restore ID` brings it back.

## JSON fields

- An issue: `id`, `title`, `description`, `design`, `acceptance_criteria`, `notes`, `type`, `priority` (0 is most urgent, 4 least), `status`, `status_reason`, `blocked`, `assignee`, `owner`, `labels`, `parent`, `ancestors` (nearest first), `due_at`, `created_at`, `created_by`, `updated_at`, `retired_at`.
- `tess show` adds `links` (`blocks`, `blocked_by`, `children`, `related`, `discovered_from`, `discovered`, each a list of IDs) and `comments`; `--history` adds `history`, and `--tree` adds `tree` (nested `id`, `title`, `status`, `children`, `blocked_by`). `tess show ID --field notes` prints one field raw.
- `tess list` and `tess ready` print an array of issues. `tess status` and `tess retire` print `{"updated": [...], "newly_unblocked": [...]}`; `newly_unblocked` is what can start now.
- An error is `{"error": {"code", "message", "hint"}}` on stderr with exit status 1. The `code` is stable, and the `hint` says what to try; for a mistyped command (`invalid_argument`) it is the command's usage line.

## Finding things

- `tess list --status waiting`, `--blocked`, `--label L`, `--assignee A`, `--parent ID`, `-s TEXT` (matches the ID, title, description and notes); `--all` includes done and omitted issues.
- `tess show ID --tree` adds the issue's children and blockers, nested, and theirs.
- `tess show ID --history` lists every change to an issue and who made it.
- IDs can be shortened to any part that is unique: `tess show 3kq`.
- Text flags take `-` to read stdin: `tess update ID --notes - --append < more.md`. Without `--append` the text replaces the field.
- `tess doctor` checks the setup (git, the database, the remote, the last sync, the hooks) and prints a `fix:` line under anything wrong; run it when `tess sync` fails or the issues look out of date.
- A `blocks` link that would close a loop is refused with `cycle`, and so is one between an issue and its own ancestor or descendant: a parent already waits for its children.
