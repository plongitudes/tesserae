---
status: accepted
date: 2026-11-20
---

# Two different issues with one ID stop the sync

Two copies can each create an issue, before either syncs, that hash to the same ID: rare with three-character IDs, but not impossible, and the adaptive length ([ADR-0017](0017-flat-immutable-ids.md)) only bounds the odds on one copy. The merge tells the case apart from the same issue seen twice (two copies that imported the same file, or a copy that lost its LKG) by `created_at` and `created_by`: equal means the same issue, which is merged against an empty LKG; different means a collision, and the sync fails with code `conflict`, naming the ID and both titles, and changes nothing. The documented fix is to retire one of the two on its own copy, recreate it, and sync again.

## Why not something else

- **Rename the later one**: IDs never change (ADR-0017), and the ID is already in commit messages, notes and other issues' links on that copy.
- **Lengthen the later one's ID** (`tess-a3f` becomes `tess-a3f7`): the same objection, and partial-ID resolution would make `a3f` ambiguous forever.
- **Keep the later one under the same ID and merge fields**: silently merges two unrelated issues into one.

## Consequences

- A refused sync is loud, which is right for something this rare: it happens once, on one copy, and the fix is one command.
- `tess doctor` can report a pending collision after a failed sync, from `sync.last_result`.
