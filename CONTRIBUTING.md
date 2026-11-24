# Contributing

## Getting set up

You need Go 1.27 or later and git. Nothing else: the linter is a tool dependency of the module and the tests bring their own git identity.

```sh
git clone https://github.com/plongitudes/tesserae
cd tesserae
scripts/build.sh      # go install ./cmd/tess, into $GOPATH/bin
tess version          # names the commit you built
```

## The gate

CI runs exactly this, and nothing else. Run it locally and you will not be surprised:

```sh
scripts/ralph/check.sh --strict
```

It runs, in order: `gofmt -l`, `go vet`, `go tool staticcheck`, the unit tests with `-count=1` and a failure on any skipped test, a build of the binary into `bin/`, then the acceptance suite: the fifteen scripts under `acceptance/testdata/`, the simulator's scenarios, and twenty-five random runs. Without `--strict` the acceptance results are reported but not required and the simulator runs five random runs; that is the quick loop while you work. The whole thing takes a couple of minutes.

The script lives under `scripts/ralph/` because it was the gate of the agent loops that built tess (below); it is still the one gate.

## Layout

```
cmd/tess/              main: os.Exit(cli.Main())
internal/cli/          the command line: cobra commands, output shapes, hooks, doctor, setup
internal/tracker/      the tracker's behaviour: issues, status, links, readiness, history
internal/store/sqlite/ the schema, views and transactions
internal/ids/          ID generation
internal/sync/         snapshot, merge, structure repair, the lock, Run
internal/sync/gitref/  the git-ref transport
internal/git/          the git plumbing tess runs: objects, refs, remotes, fast-import
acceptance/            the acceptance suite: txtar scripts, the simulator, and its README
docs/                  requirements, design, ADRs, research, history
scripts/ralph/         the gate and the agent loop that built loops 1 and 2
```

`docs/design/module-map.md` says what each package owns and what it may import. The two rules worth knowing before reading: commands reach the database only through `tracker.Tracker`, so there is one code path per command; and only `internal/sync/gitref` knows the ref names, so moving them touches nothing else.

## How tess was built, and what that means for a change

Loops 1 (the core) and 2 (sync) were built by an autonomous agent re-run on the same prompt and plan until the gate passed, against an acceptance suite the agent could not edit ([docs/loop-pattern.md](docs/loop-pattern.md)). The suite is the contract: `acceptance/README.md` fixes the JSON shapes, the error codes and the output rules, and the scripts and the simulator check them. A change that alters behaviour starts there, not in the code.

- **A new behaviour** gets a line in the design doc it belongs to (`docs/design/cli.md` for the surface, `sync.md` for sync), a script or scenario that fails, then the code.
- **A design change** that is hard to reverse, surprising without context, and a real trade-off gets an ADR in `docs/adr/`, numbered next. Most changes are none of those three and get none.
- **A new word**, or a word used in a new sense, goes in `GLOSSARY.md` first. The glossary carries no implementation detail; it is only the words.

## Testing conventions

What the loops learned, written down so it is not learned again:

- **Test names are sentences.** `TestHookSessionStartKeepsTheTimeOfTheLastSyncWhenThisOneFails` reads as the rule it checks, and a failing test names the broken rule in the report.
- **Seams, not mocks.** `TESS_NOW` fixes the clock, `TESS_ACTOR` the actor, `readBuildInfo` the build; a test substitutes a package variable and restores it in `t.Cleanup`. There is no interface whose only implementation is a fake.
- **The simulator's invariants beat goldens.** `acceptance/sim` runs random operation sequences across copies and checks what must hold afterwards (every copy converges, no event is lost, the later event won, the structure is sound). When sync misbehaves, write the scenario that shows it in the DSL there before touching the merge.
- **testscript paths resolve against the script's working directory.** After a `cd`, name files as `$WORK/file`. Environment set with `env` persists across `cd`, so unset what you set (`env TESS_ACTOR=`).
- **A git process gets a `WaitDelay`.** Every git invocation has one, so a hung credential helper cannot hang tess; `exec.ErrWaitDelay` after a clean exit is success.
- **Help is tested as syntax.** Every command needs an Examples section whose lines parse against the command's own flags, and a synopsis in POSIX notation. `internal/cli/help_test.go` enforces both.

## Style

gofmt and staticcheck decide formatting and the obvious. Beyond that:

Comments carry *why*, not *what*, and many record a thing that was tried and the observation that ruled it out. When you change code that has one, change the reasoning rather than deleting it. Doc comments cite the ADR or design section they implement, `(ADR-0022, sync.md "Structure")`, so a reader can find the decision.

The vocabulary is settled in [GLOSSARY.md](GLOSSARY.md), and the code, the help and the docs keep to it. The words that are most often said another way:

| say | not |
|---|---|
| issue | bead, ticket, task (a task is one issue type) |
| ready, waiting | unblocked, available, pending |
| held | on hold, paused, deferred |
| omit | cancelled, won't fix |
| retire | delete, archive |
| copy | replica, node, peer |
| last known good | base, ancestor |
| event | audit entry, log line |

Markdown prose is not hard-wrapped: one paragraph, one line.

## Releasing

Not yet. tess is unreleased and installed from `main` with `go install ...@latest`. When it is released: versions follow semver 0.x as the changelog states, each release is an annotated tag `vX.Y.Z` whose message is its changelog section, and the version `tess version` prints comes from the tag through Go's build information, so nothing in the code changes at a release. A release workflow and a script to prepare one will come with the first tag.

## Issues

Work is tracked in tess itself, in this repository's database, which is not committed; `tess ready` lists what is open. If you are opening a pull request from outside, a plain GitHub issue is fine.

`AGENTS.md` is instructions for AI coding agents working in this repository. It is not a contributor guide, and its workflow rules are not aimed at you.
