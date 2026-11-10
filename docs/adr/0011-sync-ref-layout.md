---
status: accepted
date: 2026-11-04
---

# Sync ref holds one file per issue and per comment

The sync ref's tree is:

```
config.json
issues/<id>.json                   scalars, labels, dependencies
comments/<id>/<comment-id>.json
events/<id>/<event-id>.json        ADR-0013
texts/<hash>                       ADR-0014
```

Git deduplicates unchanged files, so a sync writes only what changed; the 3-way merge runs only on issues both sides touched; comments and events never conflict because each is a new file; and `git log -- issues/<id>.json` on the ref is one issue's history. The store keeps no change tracking: sync serializes every issue, computes git blob hashes in-process, and lets git's tree comparison find what changed.

## Considered options

- One JSONL file (the old tool's layout): every sync rewrites the whole file (~5 MB at 10K issues), history balloons, and every merge parses everything.
- One file per issue with comments embedded: fewer files, but concurrent comments on one issue need a merge.
