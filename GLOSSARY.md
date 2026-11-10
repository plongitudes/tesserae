# tesserae

A local-first issue tracker for a developer and their coding agents, shared across worktrees and machines.

## Language

### Issues and their state

**Issue**:
A single unit of trackable work, with a title, status, priority, and links to other issues.
_Avoid_: bead, tessera (project name only), task (a task is one issue type), ticket

**Ready**:
Not started. Setting an issue to ready puts it in the queue; it shows as ready only while it isn't blocked and has no open children, and as waiting otherwise.
_Avoid_: unblocked, available, rdy

**Waiting**:
How a ready issue shows while it's blocked or has open children. Derived, never set.
_Avoid_: wtg, pending

**Blocked**:
The condition of having an active blocker, or an ancestor that is blocked or held. Any issue can be blocked; for not-started issues it shows as waiting.

**Active blocker**:
An issue that another issue depends on via a `blocks` link and that isn't done or omitted.

**In progress**:
Someone is working on the issue.
_Avoid_: ip, wip, started

**Pending review**:
The work is finished and waiting for someone to review it.
_Avoid_: in review, rev

**Held**:
Deliberately not moving, with a reason: a pause by choice, or a wait on something outside the tracker.
_Avoid_: deferred, on hold, stuck, paused

**Done**:
Finished and accepted.
_Avoid_: closed, final, complete

**Omit**:
Real work that was dropped without being done. Stays in history.
_Avoid_: cancelled, won't fix

**Retired**:
An issue that shouldn't have existed (a mistake, duplicate, or test), hidden everywhere but still restorable. Not a status: any issue can be retired.
_Avoid_: deleted, archived, tombstone

**Label**:
A freeform tag on an issue, for grouping and filtering. Labels never change status, readiness, or type; the tool gives no label a built-in meaning.
_Avoid_: tag (in docs), category

**Event**:
A recorded change to one attribute of one issue: who, when, old value, new value.
_Avoid_: audit entry, log line

### Links between issues

**Dependency**:
A typed, directed link from one issue to another.
_Avoid_: relation, edge (in prose)

**Readiness-affecting dependency**:
A `blocks` or `parent` dependency: the only types that can make an issue blocked or form a cycle.

**Parent / child**:
The two ends of a `parent` dependency. A parent can have any number of children; a child has at most one parent. IDs don't reflect the hierarchy.
_Avoid_: epic/subtask (epic is an issue type)

### Sharing

**Sync**:
Exchanging issues with other copies of the same tracker over git.

**Bridge**:
A connection that maps issues to and from a remote tracker (GitHub, Jira, and so on). Remote trackers are never the store.
_Avoid_: backend, plugin, integration
