---
status: accepted
date: 2026-11-09
---

# Hierarchy rules come from structure, not issue type

`epic` is a value of the `type` field with no behaviour; any issue can have children. Every hierarchy rule keys off whether an issue has open children (descendants that aren't done, omitted, or retired):

- An issue with open children can't be marked `done` or `omit`; the command fails with `has_open_children` and lists them. No cascade and no `--force` in v1 (an explicit `--with-children`, logged per child, can come later).
- An issue with open children isn't ready: it displays as `waiting`, because it's waiting on them, and becomes `ready` (to review and close) when they finish. This is one more clause in the readiness view, which stays the only definition. It is not a blocking rule: open children don't make the parent `blocked`, so it doesn't appear in `list --blocked`, and nothing propagates to its other children. `blocked` means an active `blocks` blocker or a blocked/held ancestor, and nothing else.
- One parent per issue (ADR-0015), so hierarchies are trees. Cycles can't be created (`link` and `import` refuse them), so there's no user-facing depth limit; recursive queries keep an internal guard of ~100 levels.

## Considered options

- Rules keyed on `type = epic`: a "task" with children would escape them, and type and structure would have to be kept in agreement.
- Closing a parent cascades to its children, with validation after: more machinery than refusing up front.
