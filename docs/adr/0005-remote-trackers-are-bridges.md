---
status: accepted
date: 2026-11-02
---

# Remote trackers are bridges, not stores

None of the remote trackers we'd integrate with (GitHub, Jira, Linear, ShotGrid, Perforce, GitLab) can compute ready work server-side, so each would need a local store in front of it anyway. So the local SQLite store is always the source of truth for queries, and remote trackers connect as bridges that map issues in and out. How bridges are packaged (e.g. separate `<binary>-bridge-<name>` executables speaking JSON) is still open.
