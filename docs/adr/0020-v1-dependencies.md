---
status: accepted
date: 2026-11-12
---

# v1 depends on four modules, fixed before the build loop

`go.mod` is written and pinned before loop 1 runs, and the loop may not change it. The build loop runs offline ([LOOP_PATTERN.md](../../LOOP_PATTERN.md)), so a dependency it wants mid-iteration can't be fetched; deciding the set up front turns "the loop quietly picks a library" into a recorded choice.

| Module | For | Why this one |
|---|---|---|
| `github.com/ncruces/go-sqlite3` v0.37 | the store | [ADR-0006](0006-ncruces-sqlite-driver.md) |
| `github.com/spf13/cobra` v1.10 | commands, flags, `--help` | Short and long flags (`-t`/`--type`), an `Example` field per command for the example-rich help [ADR-0019](0019-agent-context-integration.md) calls for, and unknown commands and flags surface as errors we can map to `invalid_argument`. The old `bd` used it, so its structure is familiar. |
| `github.com/oklog/ulid/v2` v2.1 | event IDs | [ADR-0013](0013-field-level-event-log.md) specifies ULIDs; sortable by time, no coordination. |
| `github.com/rogpeppe/go-internal` v1.16 | the acceptance harness (`testscript`) | Test-only. The old `bd` behavioural spec was testscript files too. |

Everything else is the standard library: SHA-256 and base36 for issue IDs ([ADR-0017](0017-flat-immutable-ids.md)) and text hashes ([ADR-0014](0014-content-addressed-text.md)), `encoding/json`, ANSI escape codes written directly for colour, `golang.org/x/term`-free TTY detection via `os.File.Stat` mode bits.

## Rules

- Adding a dependency is a design decision: a new ADR, or an amendment here, made by a human outside the loop.
- `go.mod` and `go.sum` are locked for the loop. `go mod tidy` and `go get` are not run inside it; the host fills the module cache from `go.mod` before each iteration.

## Considered options

- Standard-library `flag` only: no short/long aliases, subcommand plumbing and help layout written by hand; more code for the loop to get subtly inconsistent across 20 commands.
- `alecthomas/kong`: compact and declarative, but less familiar, and its help layout is harder to shape into the examples-first form we want.
- A colour or terminal library (`fatih/color`, `lipgloss`): output is six symbols and a dim header; a dependency for that is more surface than value.
