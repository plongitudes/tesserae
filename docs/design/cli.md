# CLI surface (draft)

Status: agreed 2026-11-05, pending review of the whole draft. Compatible with `bd` in spirit only ([ADR-0008](../adr/0008-cli-compatible-in-spirit.md)). Goals: few commands, readable dependency direction, one obvious way to do each thing, and JSON that agents can rely on.

## Commands

```
tess init [--prefix P]                 create the database for this repo (prefix defaults to repo dir name);
                                       the file is <git common dir>/tess.db (ADR-0003)

tess create TITLE [flags]              create an issue; prints its ID
    -t, --type TYPE                    task (default) | bug | feature | epic | chore
    -p, --priority 0-4                 default 2
    -d, --description TEXT             or "-" to read stdin
    --design TEXT                      or "-" to read stdin
    --acceptance TEXT                  or "-" to read stdin
    --notes TEXT                       or "-" to read stdin
    -l, --label L                      freeform tag with no specific built-in meaning; user-definable
										for arbitrary organization, repeat flag to create multiple
										labels at once.
    --parent ID                        create as a child (its ID is still flat)
    -b, --blocked-by ID                repeatable; adds dependencies to other issue(s).
    -a, --assignee NAME

tess show ID...                        full detail incl. dependencies and comments
    --history                          add every change, oldest first (ADR-0013)
    --tree                             add the issue's children and blockers, nested, and theirs
    --field F                          print one field's raw value (one ID); agents pipe it to a file
tess ready [-n N]                      issues with status ready, priority order (default N=10);
                                       on a TTY with no results, says so on stderr (silent when piped)
tess list [filters]                    everything else
    --status S, --type T, --label L, --assignee A, --parent ID (descendants)
    --blocked                          blocked issues of any status, with their blockers
    -s, --search TEXT                  id/title/description/notes substring
    --all                              include done/omit (default: excluded; retired never shown)
    --retired                          only retired issues

tess update ID [flags]                 any non-status field
    --title, -t/--type, -p/--priority, -d/--description, --design, --acceptance, --notes,
    -a/--assignee, --owner, --due DATE, --add-label L, --remove-label L
                                       text flags take "-" for stdin; an empty value clears a field
    --append                           text flags add a paragraph to the field instead of replacing it
tess status ID... STATUS [-r REASON]   ready | in-progress | pending-review | held | done | omit
                                       (ADR-0018). held/omit need -r. done refuses if blocked unless
                                       --force; done/omit refuse with open children. Prints issues
                                       newly unblocked. Changes nothing but status.
tess retire ID... [-r REASON]          hide permanently (ADR-0010); prints issues newly unblocked
tess restore ID                        un-retire (exact ID only)

tess link A blocks B                   B is blocked until A is done or omitted
tess link B blocked-by A               same link, inverse wording
tess link A parent-of B                (or: B child-of A)
tess link A related B
tess link B discovered-from A          B was found while working on A
tess link A B C blocks D E             several IDs on either side: the link type splits them,
                                       and every pair on the left × right becomes a link
tess unlink A TYPE B                   same forms as link

tess comment ID TEXT                   or "-" to read stdin
tess import PATH                       old bd issues.jsonl
tess sync [--remote NAME] [--dry-run] fetch, merge, push the sync ref (ADR-0012, sync.md);
                                       remote defaults to origin, else the only remote;
                                       --dry-run fetches and reports, writes nothing
tess doctor [--remote]                 check the setup: git, database, remote, sync state, hooks,
                                       other trackers; --remote also tests the remote is reachable
tess setup claude [--check|--remove]   write the Claude Code skill + hooks (ADR-0019)
tess hook session-start|session-end    plumbing called by those hooks; not shown in orientation
tess                                   no arguments: orientation, one line per working command
                                       (everything above except init, import, setup, hook)
```

`tess hook` is what `setup claude` installs in `.claude/settings.json`: `SessionStart` (matcher `startup|resume|compact`) runs `tess hook session-start`, which reads the hook's JSON from stdin, runs `tess sync` with a short timeout unless `source` is `compact`, then prints 3–6 lines of facts (ready count, in-progress count, last sync) and never instructions; it prints nothing when the repo has no database and always exits 0. `SessionEnd` runs `tess hook session-end`, which starts a detached `tess sync` and exits at once. Both commands are wrapped so a missing `tess` binary fails quietly (ADR-0019, scope notes). The sync in `session-start` has a ten-second deadline; `no_remote`, `offline`, `busy` and the deadline are reported in the `Last sync` fact, never as a failure ([sync.md](sync.md), Hooks).

Not in v1: `stats`, `export`, `quickstart`/`prime` (agent context integration), `cycles` (`link` refuses to create one; `import` reports any it finds).

## Output

- Human output by default: single-width Unicode status symbols, always; semantic colour only when stdout is a TTY. No emoji (double-width, colour, inconsistent across terminals).

  ```
  ○ ready          ◐ in-progress     ✓ done
  ⊖ waiting        ≈ pending-review  ✗ omit
  ❄ held
  ```

  `⊖` is the no-entry road sign; `≈` is "approximately right, not yet confirmed"; `✓`/`✗` are a matched pair. Errors print `error:`, never `✗`, so an omitted issue can't be mistaken for a failure. `⚠` is reserved for warnings.
- `--json` on every command. Issues are objects with snake_case fields; lists are arrays; mutating commands return the affected issue(s), and `status`/`retire` return `{"updated": [...], "newly_unblocked": [...]}`. `status` is the displayed status (incl. derived `waiting`/`ready`), plus a `blocked` boolean. Exact shapes: [acceptance/README.md](../../acceptance/README.md).
- Every `--help` has examples. Short factual notes may go to stderr when true (e.g. unsynced changes), never on every command.
- Help follows man-page conventions (POSIX utility argument syntax, man-pages(7)), in plain text (added 2026-11-17). The `Usage:` line is a full synopsis: `[ ]` optional, `...` repeatable, `|` alternatives, every flag that takes a value shown with its placeholder, no `[flags]`. A command with two forms gets two synopsis lines. Placeholders are CAPS words named for the value, not the type: `TITLE`, `ID`, `TYPE`, `N` (a count or priority), `LABEL`, `NAME` (a person), `TEXT` (a long text; `-` for stdin stays in the description), `DATE`, `REASON`, `STATUS`, `FIELD`, `PATH`. The flags table uses the same placeholders (`-l, --label LABEL`) and never a Go type name (`string`, `stringArray`, `int`). The `usage:` line of a command-line error is the synopsis. Real `man` pages are not v1 (cobra's generator pulls in a new module, ADR-0020).

  ```
  tess create TITLE [-t TYPE] [-p N] [-l LABEL]... [--parent ID] [-b ID]... [-a NAME] [-d TEXT] [--design TEXT] [--acceptance TEXT] [--notes TEXT]
  tess link ID... TYPE ID...
  tess status ID... STATUS [-r REASON] [--force]
  tess show ID... [--history] [--tree]
  tess show ID --field FIELD
  ```
- Errors go to stderr. With `--json`, stderr is `{"error": {"code": "...", "message": "...", "hint": "..."}}` and `code` is stable (`not_found`, `ambiguous_id`, `cycle`, `retired`, ...). Exit status is non-zero.
- Two kinds of error, two shapes in plain mode. A command-line error (wrong number of arguments, unknown command or flag, bad flag value; code `invalid_argument`) prints `error: ...`, then `usage: tess link A TYPE B` (the command's use line), then `run 'tess link --help' for examples`; in JSON the `hint` is the use line. A refusal from the tracker (`not_found`, `cycle`, `blocked`, ...) prints `error: ...` and a `hint:` naming the next command to try. Usage for syntax, hints for next steps, as git and cargo do.
- `tess ready` on a TTY with no results prints `no ready issues` and a hint on stderr; when piped it prints nothing, so pipelines stay clean.
- `tess sync` prints one `resolved ...` or `dropped ...` line per conflict it settled, then one summary line (`fetched 12 issues, pushed 3, resolved 1`, or `up to date`). `tess doctor` prints one line per check at one of four levels, `ok`, `info`, `warn`, `fail`, with a `fix:` line under a warn or fail; `info` is a fact that is not a problem (no remote, never synced). Exit 1 only on a `fail`. Shapes and codes: [sync.md](sync.md), [acceptance/README.md](../../acceptance/README.md).
- Partial IDs are accepted everywhere an ID is.
- Lists (`ready`, `list`) are hierarchical by default on a TTY: each issue appears under its ancestors, which show as dimmed section headers (context, not results), children indented. `--flat` gives one line per issue with no headers, and is the default when stdout isn't a TTY, so `grep`/`awk` pipelines just work; `--tree` forces the hierarchy. `-n` counts results, not headers. Results sort by priority within each group; groups sort by their best result.
- `--json` is always a flat array; each issue carries `parent` (and `ancestors`), and nesting is left to the consumer.

## Decisions folded in (accepted 2026-11-04)

- **Delete:** there isn't one; issues are retired ([ADR-0010](../adr/0010-retire-instead-of-delete.md)).
- **Config:** the prefix and any settings live in a `config` table in the database. They travel with sync as part of the snapshot.
- **Events:** every change is logged field by field ([ADR-0013](../adr/0013-field-level-event-log.md)); `show --history` reads it.

## Notes

- Status changes go through `tess status` only, not `update` ([ADR-0018](../adr/0018-one-status-command.md)). `waiting` can't be set; it's how `ready` displays when blocked.
- `--tree` means the same thing on `list` and `show`: place the issue in its hierarchy. `list --tree` puts results under their ancestors; `show --tree` prints the issue (whose `ancestors` it already lists) and then what hangs off it, children and blockers, nested. There is no separate `tree` command (dogfood checkpoint, 2026-11-16).
- A `blocks` link between an issue and its own ancestor or descendant is refused with code `cycle` in both directions ([ADR-0016](../adr/0016-hierarchy-rules.md)): a child already holds its parent, and a blocked ancestor blocks its descendants, so either direction is a dependency the issue could never satisfy.
- No ordinary command mentions sync state. "Issues changed since the last sync" appears in `doctor` and the session-start facts, nowhere else (loop 2 design session, 2026-11-20).
- `show --field F` prints the raw value: text exactly as stored, scalars plainly, `null` as nothing; with `--json`, the value as JSON. `F` is any key of the issue object, so `tess show ID --field notes > notes.md` and `tess update ID --notes - --append < more.md` round-trip without re-sending.
