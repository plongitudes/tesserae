---
status: accepted
date: 2026-11-02
---

# Core is written in Go

Agents call the tool constantly, so start-up time and a single static binary matter more than anything else about the language. Go gives both, has a pure-Go SQLite driver (see [ADR-0006](0006-ncruces-sqlite-driver.md)), and lets most carry-over code port directly. Bridges to remote trackers don't have to be Go (see [ADR-0005](0005-remote-trackers-are-bridges.md)).
