# Agent Instructions

> **Human contributors: see [CONTRIBUTING.md](CONTRIBUTING.md) instead.** This file is instructions for AI coding agents working in this repository, and its workflow rules (including the push requirements below) are aimed at them, not at you.

This repository tracks its issues with **tess**, the tool it builds. In Claude Code the skill at `.claude/skills/tess/SKILL.md` is the full guide and loads itself when issues come up; the session-start hook prints how many issues are ready and when the last sync was. Elsewhere, the quick reference below is enough to start.

## Quick reference

```sh
tess ready                      # what can be worked on now, most urgent first
tess show ID --json             # one issue in full
tess status ID in-progress      # claim it; later: done, or pending-review
tess create "Title" -t bug -p 1 # record work you find; then tess link NEW discovered-from ID
tess comment ID "What and why"  # a decision or a finding
tess sync                       # share with the other copies
```

`tess` alone prints the orientation; `tess COMMAND --help` has every flag and examples. Add `--json` to anything whose output you will read.

## Working in this repository

- **Run the gate before you call anything done**: `scripts/ralph/check.sh --strict`. CI runs exactly that. Without `--strict` is the quick loop.
- **The acceptance suite is the contract.** `acceptance/README.md` fixes the JSON shapes, the error codes and the output rules; the scripts in `acceptance/testdata/` and the simulator in `acceptance/sim/` check them. A change in behaviour starts with a failing script or scenario and a line in the design doc it belongs to, then the code. Do not edit a script to make it pass.
- **Design lives in `docs/`.** `docs/design/cli.md` is the surface, `docs/design/sync.md` is sync, `docs/adr/` are the decisions. A decision that is hard to reverse, surprising without context, and a real trade-off gets an ADR; most changes get none. New words go in `GLOSSARY.md` first, and the glossary carries no implementation detail.
- **Keep to the vocabulary** in `GLOSSARY.md`: issue (not bead or ticket), ready and waiting, held, omit, retire, copy, snapshot, last known good.
- **Comments carry why.** Many record what was tried and what ruled it out; change the reasoning rather than deleting it. Cite the ADR or design section a piece of code implements.
- **Markdown prose is not hard-wrapped**: one paragraph, one line.
- **Releases are the user's.** Pushing a version tag will publish a release; leave tagging and tag pushes to the user unless they hand them to you.

## Landing the plane

When ending a work session, finish all of these. Work is not complete until `git push` succeeds.

1. **Record what is left**: a tess issue for any follow-up, linked `discovered-from` the issue you worked on.
2. **Run the gate** if code changed.
3. **Update issue status**: `done` or `pending-review` for finished work; `in-progress` stays only on what is truly in progress.
4. **Push**:
   ```sh
   git pull --rebase
   tess sync
   git push
   git status   # must say up to date with origin
   ```
5. **Hand off**: say what the next session needs to know.

Do not stop before pushing, and do not say "ready to push when you are"; if the push fails, resolve it and push again.
