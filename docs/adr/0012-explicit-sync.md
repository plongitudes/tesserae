---
status: accepted
date: 2026-11-05
---

# Sync runs only when asked

Commands never touch the network. `tess sync` fetches the sync ref, merges, and pushes; Claude Code hooks call it at session start (so cloud sessions begin current) and at session end (so their work gets back). Worktrees on one machine already share the database (ADR-0003), so sync only matters between machines, and that doesn't need per-command freshness. Offline just means "not synced yet".

## Rules

- Local ref updates are compare-and-swap (`git update-ref <ref> <new> <expected-old>`); a rejected push re-fetches, re-merges, and retries.
- The local ref is the last known good snapshot (GLOSSARY): it moves only after a push succeeds and the merged state is in the database, so it is always what this copy and the remote last agreed on ([sync.md](../design/sync.md), added 2026-11-20).
- A plain `git push` never carries the ref. We don't add a push refspec (it would stop `git push` pushing the current branch) or a pre-push hook.
- Issues are exactly as private as the remote: anyone who can read the repo can fetch the ref. `tess init` and the first `tess sync` warn when the remote is public. (The warning is deferred past loop 2, 2026-11-20: it needs a definition of "public" that doesn't need the network. Tracked as an issue in the tesserae tracker.)

## Considered options

- Pull on every command: adds a 0.5–2 s network round trip to every agent call and gives every command an offline failure mode.
- Background sync when the last one is older than N minutes: fresher, but it's the daemon creeping back in.
