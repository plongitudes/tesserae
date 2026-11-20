---
status: accepted
date: 2026-11-20
---

# A conflict is resolved by the later event, whoever made it

When two copies hold different values for one field of one issue, the merge takes the side whose latest event for that field since the last known good snapshot (LKG) is later, by `changed_at` and then by event ID. A side with no such event counts as time zero, so an edit on one side and nothing on the other takes the edit; when neither side has one, the side that differs from the LKG wins, then the later issue `updated_at`, then the smaller canonical JSON value. There is no three-way shortcut for a side that equals the LKG: a copy that changed a field and changed it back has the later event and wins. A conflict in the glossary's sense, both sides changed since the LKG, is the case where both sides have an event, and it is reported. The merge never asks a person to choose and never stops a sync over a conflict. Every resolution is reported, on stderr and in the JSON result, and never stored: the losing edit's own event is already in the union of events, so `show --history` shows both.

Two corollaries. A retire is a field change like any other, so the later event decides between a retire and a restore; but once an issue has resolved to retired, edits the other copy made after the retire are dropped, as a single copy would have refused them. Collection elements (labels, links) are each a unit, resolved by the events that added or removed that element, so a removal on one copy propagates instead of being resurrected by a union, and a remove-then-add on the other copy wins by its later event.

## Why not something else

- **Refuse and make the user pick**, as git does: a session-start hook would stop dead, and an agent would get a failure it cannot fix. The thing being merged is a few short fields, not code.
- **A human's edit beats an agent's**: wrong whenever the human's edit was the earlier, stale one, and it needs actors to be classifiable. The cross-actor case is the normal case here, since one developer's agents on other machines are different actors.
- **Per-field domain rules** (`done` beats `in-progress`): every rule is a surprise to learn, and none is right in every case.
- **Last write by issue `updated_at`** (the earlier module-map draft): coarser than the event log, and loses the edit to one field because another field changed later.
- **A plain three-way merge** (a side equal to the base is unchanged, the other side wins): differs from the event rule only when a copy changes a field and changes it back, and there it takes the other copy's older edit. The event rule gives the simulator a one-line invariant, "every field holds the value of its latest event anywhere"; the three-way shortcut has no such statement.
- **Record resolutions as events or comments** on the issue: the losing edit's event is already in the union, so a record would say the same thing twice.

## Consequences

- Clocks matter. Events carry wall-clock time from different machines; a solo developer's machines agree to within seconds, and two edits to one field inside that window are rare, and reported when they happen.
- Multi-human use is outside v1 ([requirements](../requirements.md)). If it comes, the rule gets revisited then, with its own ADR.
- The tie-breaks exist only for convergence: whatever they pick, every copy picks the same.
