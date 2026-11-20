---
status: accepted
date: 2026-11-20
---

# A merge drops the later link rather than break a hierarchy rule

Two clean merges can combine into a structure a single copy would refuse: a cycle through `blocks` and `parent` links, or a `blocks` link between an issue and its own ancestor or descendant ([ADR-0016](0016-hierarchy-rules.md)). After the field merge, the merged snapshot is checked against every rule a single copy enforces. For each violation, the links that take part in it and are new since the LKG are the candidates, the one with the latest event is dropped and reported, and the check repeats until the snapshot is clean. A merged database satisfies every invariant a single copy does, or readiness ([ADR-0007](0007-readiness-is-one-sql-view.md)) would be undefined on it.

## Why not something else

- **Refuse the sync**: neither copy can fix it alone, since each copy's own state is valid; the user would have to guess which link to remove and where.
- **Keep the links and let readiness cope**: the readiness view has a recursion guard, not cycle semantics; `blocked` would depend on traversal order.
- **Drop the earlier link**: the later link was made with less knowledge of the other, and dropping it is what a single copy would have done by refusing it.

## Consequences

- The dropped link's `add` event stays in the history with no matching removal. The issue row, not the log, is the source of truth (ADR-0013).
- A second parent cannot arise: `parent` is one scalar unit of the merge, so it resolves to one value.
- The check runs over all issues including retired ones, as the store's cycle check does, because a loop through a retired issue would close on restore.
