# Module map

Living design doc; vocabulary from the codebase-design skill (module, interface, seam, adapter, depth). Last revised 2026-11-03.

```
                 cli            adapter: flags, --json shapes, ui symbols
                  │
               tracker          the deep module: all issue-tracking behaviour
          ┌───────┴──────┬──────────────┐
         ids        [store seam]       sync
        (pure)           │          ┌────┴─────┐
                      sqlite      merge   [transport seam]
                  (incl. readiness (pure)        │
                      views)                   gitref ── git (plumbing only)
```

## Modules

- **tracker**: the interface callers and most tests use. Owns the close guard, "newly unblocked", ID allocation inside the create transaction, typed patches, and clock injection. Because `cli` is only an adapter over it, there's one code path per command.
- **ids**: ID generation (`<prefix>-<base36 hash>`, adaptive length, flat for all issues; [ADR-0017](../adr/0017-flat-immutable-ids.md)) and partial-ID resolution rules. Lookup is an indexed store query.
- **store seam / sqlite**: one adapter in v1. Readiness lives here as SQL views ([ADR-0007](../adr/0007-readiness-is-one-sql-view.md)). Tests use SQLite `:memory:`; there is no in-memory fake. The second adapter, if it ever comes, is a Markdown store with SQLite as its index.
- **sync**: owns the snapshot record format and merge policy. `merge` is a pure 3-way function; the base comes from `git merge-base` of the local and remote sync refs, so no base files.
- **transport seam / gitref**: the git-ref transport ([ADR-0004](../adr/0004-sync-over-unchecked-out-git-ref.md)), plus a fake for tests. Rule: only this adapter knows the ref name (`refs/tess/sync`), so moving it later (e.g. to a branch, or adding an inbox for cloud agents) touches nothing else.
- **git**: wraps only the plumbing the transport needs (hash-object, mktree, commit-tree, update-ref, fetch, push, merge-base, cat-file/ls-tree).

One versioned issue record format serves the sync snapshot, the old `issues.jsonl` import, and (probably) the bridge protocol.

## Tracker interface (draft)

```go
type Tracker interface {
    Create(ctx context.Context, n NewIssue) (Issue, error)          // ID allocated in-tx
    Update(ctx context.Context, id string, p Patch) (Issue, error)  // typed patch
    Close(ctx context.Context, id string, reason string, force bool) (Closed, error) // guard + newly unblocked
    Link(ctx context.Context, from, to string, t DepType) error     // cycle check on readiness edges only
    Unlink(ctx context.Context, from, to string, t DepType) error
    Comment(ctx context.Context, id, author, text string) (Comment, error)
    Get(ctx context.Context, partialID string) (Issue, error)       // resolves partial IDs
    List(ctx context.Context, f Filter) ([]Issue, error)            // Filter.Ready / Filter.Blocked select via the view
}
```

`ready` and `blocked` are `List` filters, not separate methods, so both go through the one readiness view. Reopen and defer are `Update` patches.

## Merge rules (draft)

Per issue file, 3-way against the `git merge-base` of the local and remote sync refs ([ADR-0011](../adr/0011-sync-ref-layout.md)):

- Scalars: if only one side changed a field relative to the base, take it; if both did, last write wins by `updated_at`.
- Labels and dependencies: set merge relative to the base, so removals propagate (the old tool's plain union resurrected them).
- `retired_at`: merged as its own field; edits elsewhere never clear it ([ADR-0010](../adr/0010-retire-instead-of-delete.md)).
- Comments: union of files; no merge needed.
- Issue present on one side only and absent from the base: it's new, keep it.
- `config.json`: per-key, same scalar rule.

## When sync runs

Only on `tess sync` ([ADR-0012](../adr/0012-explicit-sync.md)).
