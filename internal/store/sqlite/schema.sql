-- tesserae schema, version 1.
--
-- Timestamps are RFC 3339 UTC text. The tracker writes them from its clock;
-- nothing here defaults to the wall clock.

CREATE TABLE config (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) WITHOUT ROWID;

-- Write-once bodies of the large text fields, keyed by lowercase hex SHA-256
-- (ADR-0014). Rows are never updated or deleted: events point at them.
CREATE TABLE texts (
    hash TEXT PRIMARY KEY,
    body TEXT NOT NULL
) WITHOUT ROWID;

-- The empty text, so an issue's text columns always hold a hash.
INSERT INTO texts (hash, body)
VALUES ('e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855', '');

-- Only store writes and sync touch this table; reads go through live_issues
-- (ADR-0010). status holds the five stored statuses, not-started being
-- 'ready'; 'waiting' is derived and never stored (ADR-0009).
CREATE TABLE issues (
    id                  TEXT PRIMARY KEY,
    title               TEXT NOT NULL,
    description         TEXT NOT NULL REFERENCES texts (hash)
                        DEFAULT 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855',
    design              TEXT NOT NULL REFERENCES texts (hash)
                        DEFAULT 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855',
    acceptance_criteria TEXT NOT NULL REFERENCES texts (hash)
                        DEFAULT 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855',
    notes               TEXT NOT NULL REFERENCES texts (hash)
                        DEFAULT 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855',
    type                TEXT NOT NULL DEFAULT 'task',
    priority            INTEGER NOT NULL DEFAULT 2 CHECK (priority BETWEEN 0 AND 4),
    status              TEXT NOT NULL DEFAULT 'ready'
                        CHECK (status IN ('ready', 'in-progress', 'pending-review', 'held', 'done', 'omit')),
    status_reason       TEXT,
    assignee            TEXT,
    owner               TEXT,
    due_at              TEXT,
    created_at          TEXT NOT NULL,
    created_by          TEXT NOT NULL,
    updated_at          TEXT NOT NULL,
    retired_at          TEXT
) WITHOUT ROWID;

CREATE INDEX issues_by_order ON issues (priority, created_at, id);

CREATE TABLE labels (
    issue_id TEXT NOT NULL REFERENCES issues (id),
    label    TEXT NOT NULL,
    PRIMARY KEY (issue_id, label)
) WITHOUT ROWID;

CREATE INDEX labels_by_label ON labels (label, issue_id);

-- One row per link, in its canonical direction (ADR-0015):
--   blocks           from = the blocker,    to = the blocked issue
--   parent           from = the parent,     to = the child
--   discovered-from  from = the discovered, to = the origin
--   related          symmetric; stored once, as given
CREATE TABLE links (
    from_id TEXT NOT NULL REFERENCES issues (id),
    type    TEXT NOT NULL CHECK (type IN ('blocks', 'parent', 'related', 'discovered-from')),
    to_id   TEXT NOT NULL REFERENCES issues (id),
    PRIMARY KEY (from_id, type, to_id),
    CHECK (from_id <> to_id)
) WITHOUT ROWID;

CREATE INDEX links_by_to ON links (to_id, type, from_id);
-- One parent per issue.
CREATE UNIQUE INDEX links_one_parent ON links (to_id) WHERE type = 'parent';
-- A related link exists once, whichever way round it was given.
CREATE UNIQUE INDEX links_related_once ON links (min(from_id, to_id), max(from_id, to_id))
    WHERE type = 'related';

-- Immutable in v1; bodies stay inline (ADR-0014).
CREATE TABLE comments (
    id         TEXT PRIMARY KEY,
    issue_id   TEXT NOT NULL REFERENCES issues (id),
    author     TEXT NOT NULL,
    text       TEXT NOT NULL,
    created_at TEXT NOT NULL
) WITHOUT ROWID;

CREATE INDEX comments_by_issue ON comments (issue_id, created_at, id);

-- One row per changed attribute (ADR-0013). id is a ULID, so id order is
-- time order. old_value and new_value hold JSON; SQL NULL is JSON null.
CREATE TABLE events (
    id         TEXT PRIMARY KEY,
    issue_id   TEXT NOT NULL REFERENCES issues (id),
    changed_at TEXT NOT NULL,
    changed_by TEXT NOT NULL,
    attr       TEXT NOT NULL,
    old_value  TEXT,
    new_value  TEXT
) WITHOUT ROWID;

CREATE INDEX events_by_issue ON events (issue_id, id);
CREATE INDEX events_by_issue_attr ON events (issue_id, attr, id);

-- ---------------------------------------------------------------------------
-- Views. Every read goes through these.
-- ---------------------------------------------------------------------------

-- The one place retired issues are filtered out (ADR-0010).
CREATE VIEW live_issues AS
SELECT * FROM issues WHERE retired_at IS NULL;

-- The readiness-affecting dependencies between live issues: blocks and parent
-- links, both running from the issue that can hold another up (the blocker,
-- the parent) to the issue held up. A link to or from a retired issue is not
-- an edge; its row stays in links and counts again on restore.
CREATE VIEW readiness_edges AS
SELECT l.type, l.from_id, l.to_id
FROM links l
JOIN live_issues f ON f.id = l.from_id
JOIN live_issues t ON t.id = l.to_id
WHERE l.type IN ('blocks', 'parent');

-- Every (issue, ancestor) pair, depth 1 being the parent. Cycles can't be
-- created (ADR-0016); the depth guard only keeps a damaged database from
-- recursing forever.
CREATE VIEW issue_ancestors (id, ancestor_id, depth) AS
WITH RECURSIVE up (id, ancestor_id, depth) AS (
    SELECT to_id, from_id, 1 FROM readiness_edges WHERE type = 'parent'
    UNION
    SELECT up.id, e.from_id, up.depth + 1
    FROM up
    JOIN readiness_edges e ON e.type = 'parent' AND e.to_id = up.ancestor_id
    WHERE up.depth < 100
)
SELECT id, ancestor_id, depth FROM up;

-- THE definition of blocked (ADR-0007): an issue with an active blocker (a
-- blocks link from an issue that isn't done or omit), or with an ancestor
-- that is blocked or held. A held issue is not itself blocked by being held,
-- and open children never make their parent blocked (ADR-0016).
CREATE VIEW blocked_issues (id) AS
WITH RECURSIVE blocked (id, depth) AS (
    SELECT e.to_id, 0
    FROM readiness_edges e
    JOIN live_issues blocker ON blocker.id = e.from_id
    WHERE e.type = 'blocks' AND blocker.status NOT IN ('done', 'omit')
    UNION
    SELECT e.to_id, 0
    FROM readiness_edges e
    JOIN live_issues parent ON parent.id = e.from_id
    WHERE e.type = 'parent' AND parent.status = 'held'
    UNION
    SELECT e.to_id, blocked.depth + 1
    FROM blocked
    JOIN readiness_edges e ON e.type = 'parent' AND e.from_id = blocked.id
    WHERE blocked.depth < 100
)
SELECT DISTINCT id FROM blocked;

-- An issue's open children: its descendants that aren't done, omit, or
-- retired (ADR-0016). "Has open children" is EXISTS over this view.
CREATE VIEW open_children (parent_id, id) AS
SELECT DISTINCT a.ancestor_id, a.id
FROM issue_ancestors a
JOIN live_issues i ON i.id = a.id
WHERE i.status NOT IN ('done', 'omit');

-- What each live issue shows: whether it's blocked, whether it has open
-- children, and its displayed status, which is the stored status except that
-- a stored ready shows as waiting while it's blocked or has open children
-- (ADR-0009, ADR-0016).
CREATE VIEW issue_states (id, blocked, has_open_children, display_status) AS
SELECT s.id, s.blocked, s.has_open_children,
       CASE WHEN s.status = 'ready' AND (s.blocked OR s.has_open_children) THEN 'waiting'
            ELSE s.status END
FROM (
    SELECT i.id, i.status,
           i.id IN (SELECT id FROM blocked_issues)            AS blocked,
           i.id IN (SELECT parent_id FROM open_children)      AS has_open_children
    FROM live_issues i
) s;
