---
status: accepted
date: 2026-11-02
---

# Sync over a git ref that is never checked out

The default sync transport pushes and fetches a dedicated ref, `refs/tess/sync`, that no branch or worktree ever checks out. It works with any git remote (no host lock-in) and never touches code branches. Remote trackers are not sync transports; see [ADR-0005](0005-remote-trackers-are-bridges.md).

Layout: [ADR-0011](0011-sync-ref-layout.md). Deletes travel as an ordinary field change ([ADR-0010](0010-retire-instead-of-delete.md)). When it runs: [ADR-0012](0012-explicit-sync.md).

## Cloud agent environments (noted 2026-11-09, deferred)

This ref can't be pushed from Claude Code cloud sessions: their GitHub proxy "rejects branch deletions and pushes of anything other than a branch, such as a tag" ([cloud-environments, GitHub proxy](https://code.claude.com/docs/en/cloud-environments.md#github-proxy)). Other cloud agent platforms restrict pushes differently (some only to the agent's own branch), so switching to a `tess/sync` branch would fix Claude's cloud but not generalise. We briefly adopted the branch, then reverted: while the repo is private, `tess` can't be installed on a cloud VM anyway, and a branch adds branch-list clutter and CI exclusions for no current benefit.

The general fix, when wanted: an inbox. A restricted agent records its issue changes as events in a file on its own work branch (e.g. `.tess/inbox/<session>.json`), and a local `tess sync` folds merged or fetched inboxes into the sync ref and clears them. Moving the ref itself stays cheap as long as only the transport adapter knows its name.
