---
status: accepted
date: 2026-11-09
---

# Issue IDs are flat and never change

Every issue gets `<prefix>-<base36 hash>` (length grows with issue count), including issues created as children. IDs never change and never encode location; the hierarchy lives only in `parent` links and is shown by `show`, `tree`, and indented listings. One generator, one scheme.

## Considered options

- Child IDs `<parent>.<n>` (old `bd`): reparenting or adding a parent later leaves an ID naming the wrong parent, and it can't be renamed without breaking references in commits, comments, and other issues.
- A hidden permanent ID plus a renameable, hierarchical display name (Jira's model): works, but old names keep appearing outside the tool, names can never be reused, per-parent child numbers collide across offline machines, and moving a parent renames its whole subtree.

## Consequences

- The prefix is fixed for the life of the repo's issues.
