# Module map

Living design doc; vocabulary from the codebase-design skill (module, interface, seam, adapter, depth). Last revised 2026-11-20 (sync packages named, merge rules moved to [sync.md](sync.md)).

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
- **sync** (`internal/sync`): owns the snapshot format and the merge ([sync.md](sync.md)). `Merge` is a pure 3-way function over three snapshots, symmetric in ours and theirs; the LKG is the local sync ref, which the transport reads, so there are no base files. `Load` serialises a database into a snapshot and `Store` writes a merged one back, in one transaction and without logging events. `Run` is the whole sync: fetch, merge, push with retry, store, move the ref. The loop-2 simulator ([acceptance/sim](../../acceptance/sim/)) uses exactly `Snapshot`, `Merge`, `Load` and `Run`.
- **transport seam / gitref** (`internal/sync/gitref`): the git-ref transport ([ADR-0004](../adr/0004-sync-over-unchecked-out-git-ref.md)). Rule: only this adapter knows the ref names (`refs/tess/sync`, `refs/tess/remotes/<remote>/sync`), so moving them later (e.g. to a branch, or adding an inbox for cloud agents) touches nothing else. Tests use bare repositories in temp directories, not a fake: the only thing a fake would save is a few milliseconds.
- **git** (`internal/git`): runs the user's `git` binary for the plumbing the transport needs (fetch, push, cat-file, ls-tree, fast-import or hash-object/mktree/commit-tree, update-ref, rev-parse), one process per step ([ADR-0020](../adr/0020-v1-dependencies.md), amended). `cli` already has `rev-parse` helpers in `env.go` that belong here once it exists.

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

## Merge rules

In [sync.md](sync.md), "The merge": a 3-way merge per issue against the last known good snapshot, field by field, the later event winning a conflict ([ADR-0021](../adr/0021-latest-event-wins.md)); set merge for labels and links, so removals propagate (the old tool's plain union resurrected them); structure repaired after the merge ([ADR-0022](../adr/0022-merge-repairs-structure.md)); ID collisions refused ([ADR-0023](../adr/0023-id-collisions-refuse.md)). The draft that stood here (last write wins by `updated_at`) is superseded.

## When sync runs

Only on `tess sync` ([ADR-0012](../adr/0012-explicit-sync.md)).
