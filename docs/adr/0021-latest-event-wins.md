---
status: accepted
date: 2026-11-20
---

# A conflict is resolved by the later event, whoever made it

A conflict is two copies changing the same field of the same issue since the last known good snapshot (LKG). The merge takes the side whose latest event for that field is later, by `changed_at` and then by event ID; a side with no event for the field counts as time zero, and when neither has one, the later issue `updated_at` wins, then the smaller canonical JSON value. The merge never asks a person to choose and never stops a sync over a conflict. Every resolution is reported, on stderr and in the JSON result, and never stored: the losing edit's own event is already in the union of events, so `show --history` shows both.

Two corollaries. A retire is a field change like any other, so the later event decides between a retire and a restore; but once an issue has resolved to retired, edits the other copy made after the retire are dropped, as a single copy would have refused them. Collection elements (labels, links) cannot conflict at all, since relative to the LKG both sides can only add an absent element or remove a present one.

## Why not something else

- **Refuse and make the user pick**, as git does: a session-start hook would stop dead, and an agent would get a failure it cannot fix. The thing being merged is a few short fields, not code.
- **A human's edit beats an agent's**: wrong whenever the human's edit was the earlier, stale one, and it needs actors to be classifiable. The cross-actor case is the normal case here, since one developer's agents on other machines are different actors.
- **Per-field domain rules** (`done` beats `in-progress`): every rule is a surprise to learn, and none is right in every case.
- **Last write by issue `updated_at`** (the earlier module-map draft): coarser than the event log, and loses the edit to one field because another field changed later.

## Consequences

- Clocks matter. Events carry wall-clock time from different machines; a solo developer's machines agree to within seconds, and two edits to one field inside that window are rare, and reported when they happen.
- Multi-human use is outside v1 ([requirements](../requirements.md)). If it comes, the rule gets revisited then, with its own ADR.
- The tie-breaks exist only for convergence: whatever they pick, every copy picks the same.
