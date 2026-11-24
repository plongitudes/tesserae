# tesserae

An issue tracker for coding agents: one SQLite file per repository, synced through a git ref.

`tess` keeps a repository's issues in a single database inside git's common directory, so every worktree of the repository sees the same issues and nothing is added to the working tree. It is built to be driven by a coding agent as much as by a person: every command takes `--json` and prints stable shapes, every error carries a code and a hint, and `tess setup claude` gives Claude Code a skill and two hooks so the tracker stays in the agent's context without a word of instruction in your prompts.

Issues move between machines with `tess sync`, which exchanges them through a git ref that no branch ever checks out (`refs/tess/sync`). Any git remote works, there is no server, and a conflict is settled by a rule rather than a prompt: the later edit wins, field by field, and the sync tells you what it decided.

## Requirements

- git 2.29 or later on `PATH`. tess runs the user's git for its plumbing and keeps no git library of its own.
- Go 1.27 or later, to install it. The binary is static; the machine that runs it needs only git.

## Install

```sh
go install github.com/plongitudes/tesserae/cmd/tess@latest
```

That puts `tess` in `$GOBIN`, or `$GOPATH/bin` (usually `~/go/bin`); make sure it is on your `PATH`. From a checkout, `scripts/build.sh` does the same with the commit you have. `tess version` says which build you are running.

## Quick start

```sh
cd your-repo
tess init --prefix api                      # the database: <git common dir>/tess.db
tess create "Fix the login redirect" -t bug -p 1
tess ready                                  # what can be worked on now, most urgent first
tess status api-7kq in-progress
tess status api-7kq done
tess sync                                   # share with the other copies through origin
tess doctor                                 # check the setup; --remote also checks the remote
```

IDs are the prefix and a short hash, `api-7kq`, which grows a character as the tracker fills; any part of an ID that names one issue is enough. `tess` alone prints a one-screen orientation; `tess COMMAND --help` has every flag and examples.

## Commands

| Command | What it does |
|---|---|
| `tess ready` | issues that can be worked on now, by priority |
| `tess create "Title" -p 1` | add an issue; prints its ID |
| `tess show ID` | one issue in full; `--tree`, `--history`, `--field F` |
| `tess update ID --notes "..."` | change fields; `--append` adds to a text |
| `tess status ID in-progress` | or `ready`, `pending-review`, `held`, `done`, `omit` |
| `tess comment ID "Text"` | add a comment |
| `tess link A blocks B` | B waits for A; also `parent-of`, `related`, `discovered-from` |
| `tess unlink A blocks B` | remove a link |
| `tess list --status waiting` | every other view: filters, `-s`, `--all`, `--retired` |
| `tess retire ID` / `tess restore ID` | take a mistaken issue out of every view, and bring it back |
| `tess sync` | exchange issues with the other copies; `--dry-run` |
| `tess doctor` | check the setup and say what to fix; `--remote` |
| `tess init`, `tess import`, `tess setup`, `tess version` | run once per repository, or when something is wrong |

An issue is *ready* when nothing blocks it and none of its children are open; a ready issue that is blocked shows as *waiting*. `done` refuses while an issue is blocked or has open children, so the tree stays honest. The full surface is in [docs/design/cli.md](docs/design/cli.md); the words are in [GLOSSARY.md](GLOSSARY.md).

## With Claude Code

```sh
tess setup claude
```

This writes two files in the worktree, meant to be committed so every clone and every agent gets them:

- `.claude/skills/tess/SKILL.md`, a skill that tells Claude Code when and how to use tess: the work loop, the statuses, the links.
- `.claude/settings.json`, which allows `tess` commands and adds two hooks. At the start of a session, `tess hook session-start` syncs (with a ten-second deadline) and prints a few facts: how many issues are ready, how many in progress, when the last sync was. At the end, `tess hook session-end` starts a sync in the background. Both are wrapped so a machine without tess stays quiet.

`tess setup claude --check` says whether the files are current, and `--remove` takes them out. Changes made by a Claude Code session are recorded as `claude:<session>`; set `TESS_ACTOR` to record them as something else.

## Sync

`tess sync` fetches the remote's ref, merges three snapshots (the last one this copy synced, this copy's database, the remote's), pushes the result with `--force-with-lease`, and only then writes the merge into the database and moves the local ref. Nothing is written locally before the push succeeds, so a failure part-way leaves the copy exactly as it was.

Merging is by field, and the later change to a field wins; every change stays in the issue's history either way. A link that would make a dependency loop is dropped and reported. Two copies that happen to create different issues under the same ID make the sync refuse with `conflict`, naming both, until one of them is recreated. The reasons a sync can fail are codes you can act on: `no_remote`, `auth_failed`, `host_unresolved`, `connection_refused`, `remote_not_found`, `push_rejected`, `prefix_mismatch`, `busy` and a few more, each with a hint. The design is in [docs/design/sync.md](docs/design/sync.md).

## Design and history

- [docs/requirements.md](docs/requirements.md): what the tool must do and for whom.
- [docs/design/](docs/design/): the CLI surface, the module map, sync, operations.
- [docs/adr/](docs/adr/): the decisions, with the alternatives they rejected.
- [GLOSSARY.md](GLOSSARY.md): the vocabulary, which the code and the docs keep to.
- [docs/history/](docs/history/): where the project came from. tess began as a rewrite of a fork of [beads](https://github.com/steveyegge/beads); `tess import` reads a beads `issues.jsonl`.

## Development

```sh
scripts/ralph/check.sh --strict
```

That is the whole gate, and what CI runs: gofmt, vet, staticcheck, the unit tests, a build, the acceptance scripts and the simulator. [CONTRIBUTING.md](CONTRIBUTING.md) has the rest. Most of tess was built by autonomous agent loops against a locked acceptance suite; [docs/loop-pattern.md](docs/loop-pattern.md) describes how.

## License

[MIT](LICENSE)
