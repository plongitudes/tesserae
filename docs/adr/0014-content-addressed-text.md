---
status: accepted
date: 2026-11-09
---

# Large text fields are stored by content hash

Description, design, acceptance criteria, and notes live in a write-once `texts(hash, body)` table; the issue row holds the hash. An edit writes one new text row and leaves the old one, so every version is kept, events stay small ([ADR-0013](0013-field-level-event-log.md) logs hashes), and identical text is stored once. Old texts are never deleted, because history points at them.

Titles stay inline (short, shown in every list). Comment bodies stay inline in their own table while comments are immutable.

## Consequences

- List queries (`list`, `ready`, `blocked`) scan narrower rows and get no slower, probably faster; `show` adds one indexed lookup per text field; `search` joins to current texts (FTS5 can come later).
- The sync ref mirrors it: `texts/<hash>` files, with issue and event files referencing hashes, so a status change doesn't re-send the description.
