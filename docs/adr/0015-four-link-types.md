---
status: accepted
date: 2026-11-09
---

# Four link types

A link type exists only if it behaves differently, or carries information a label can't:

| Type | Behaviour |
|---|---|
| `blocks` | affects readiness |
| `parent` | hierarchy; blocked and held propagate to descendants; one parent per issue |
| `related` | none |
| `discovered-from` | none (a directed `related`: "found while working on X") |

Each link is stored once, with a direction; inverse wording is accepted and displayed (`A blocks B` ≡ `B blocked-by A`, `A parent-of B` ≡ `B child-of A`). Only `blocks` and `parent` take part in readiness and cycle detection.

## Considered options

- `duplicates` and `supersedes` (old `bd`): behave exactly like `related`. A duplicate is retired with a `related` link and a reason.
- Dropping `discovered-from` for `related` plus a label: a label can't say which side was found during the other, and that provenance matters with parallel agents.
- Making discovered work a child of its origin: discovered work is usually out of the origin's scope, would inherit its holds and blocks, and would use up its one parent slot.
