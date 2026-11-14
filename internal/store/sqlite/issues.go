package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

// timeLayout is how every timestamp is stored: RFC 3339, UTC, always with
// milliseconds, so that text order is time order.
const timeLayout = "2006-01-02T15:04:05.000Z"

// FormatTime renders t the way the store holds timestamps.
func FormatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// ParseTime reads a stored timestamp (any RFC 3339 form) as UTC.
func ParseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	return t.UTC(), err
}

// Issue is an issue as it reads: the issue object of acceptance/README.md.
// Status is the displayed status, and Blocked the computed condition, both
// from the issue_states view. Links, Comments, PreviousStatus and History
// are nil unless loaded (LoadDetail, LoadHistory) and are left out of the
// JSON when nil.
type Issue struct {
	ID                 string     `json:"id"`
	Title              string     `json:"title"`
	Description        string     `json:"description"`
	Design             string     `json:"design"`
	AcceptanceCriteria string     `json:"acceptance_criteria"`
	Notes              string     `json:"notes"`
	Type               string     `json:"type"`
	Priority           int        `json:"priority"`
	Status             string     `json:"status"`
	StatusReason       *string    `json:"status_reason"`
	Blocked            bool       `json:"blocked"`
	Assignee           *string    `json:"assignee"`
	Owner              *string    `json:"owner"`
	Labels             []string   `json:"labels"`
	Parent             *string    `json:"parent"`
	Ancestors          []string   `json:"ancestors"`
	DueAt              *time.Time `json:"due_at"`
	CreatedAt          time.Time  `json:"created_at"`
	CreatedBy          string     `json:"created_by"`
	UpdatedAt          time.Time  `json:"updated_at"`
	RetiredAt          *time.Time `json:"retired_at"`

	Links          *Links    `json:"links,omitzero"`
	Comments       []Comment `json:"comments,omitzero"`
	PreviousStatus *string   `json:"previous_status,omitzero"`
	History        []Event   `json:"history,omitzero"`
}

// Links are the IDs an issue is linked to, by the role the other issue
// plays, each sorted. Retired issues are left out; their link rows persist.
type Links struct {
	Blocks         []string `json:"blocks"`
	BlockedBy      []string `json:"blocked_by"`
	Children       []string `json:"children"`
	Related        []string `json:"related"`
	DiscoveredFrom []string `json:"discovered_from"`
	Discovered     []string `json:"discovered"`
}

// Comment is an immutable remark on an issue.
type Comment struct {
	ID        string    `json:"id"`
	Author    string    `json:"author"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

// NewIssue is a fully decided issue to insert: the tracker has allocated the
// ID, applied defaults, and resolved the IDs it links to. The fields after
// CreatedBy are for an issue that arrives with a past (an import); left zero,
// the issue is ready, unowned, live, and last updated when it was created.
type NewIssue struct {
	ID                 string
	Title              string
	Description        string
	Design             string
	AcceptanceCriteria string
	Notes              string
	Type               string
	Priority           int
	Assignee           string // "" for none
	Labels             []string
	Parent             string   // "" for none
	BlockedBy          []string // the blockers' IDs
	CreatedAt          time.Time
	CreatedBy          string

	Status       string // "" for ready
	StatusReason string // "" for none
	Owner        string // "" for none
	DueAt        time.Time
	UpdatedAt    time.Time // zero for CreatedAt
	RetiredAt    time.Time
}

// InsertIssue writes a new issue with its texts, labels, and the links it is
// created with. It logs no events: creation is recorded by created_at and
// created_by on the issue (ADR-0013). Repeated labels and blockers are
// written once.
func InsertIssue(ctx context.Context, tx *sql.Tx, n NewIssue) error {
	var hashes [4]string
	for i, body := range []string{n.Description, n.Design, n.AcceptanceCriteria, n.Notes} {
		h, err := putText(ctx, tx, body)
		if err != nil {
			return err
		}
		hashes[i] = h
	}
	status, updatedAt := n.Status, n.UpdatedAt
	if status == "" {
		status = "ready"
	}
	if updatedAt.IsZero() {
		updatedAt = n.CreatedAt
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO issues (id, title, description, design, acceptance_criteria, notes,
		                     type, priority, status, status_reason, assignee, owner, due_at,
		                     created_at, created_by, updated_at, retired_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.ID, n.Title, hashes[0], hashes[1], hashes[2], hashes[3],
		n.Type, n.Priority, status, nullable(n.StatusReason), nullable(n.Assignee), nullable(n.Owner),
		nullableTime(n.DueAt), FormatTime(n.CreatedAt), n.CreatedBy, FormatTime(updatedAt), nullableTime(n.RetiredAt))
	if err != nil {
		return fmt.Errorf("insert issue %s: %w", n.ID, err)
	}
	for _, label := range n.Labels {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO labels (issue_id, label) VALUES (?, ?)`, n.ID, label); err != nil {
			return fmt.Errorf("label issue %s: %w", n.ID, err)
		}
	}
	if n.Parent != "" {
		if err := InsertLinkUnlogged(ctx, tx, Link{From: n.Parent, Type: LinkParent, To: n.ID}); err != nil {
			return err
		}
	}
	for i, blocker := range n.BlockedBy {
		if slices.Contains(n.BlockedBy[:i], blocker) {
			continue
		}
		if err := InsertLinkUnlogged(ctx, tx, Link{From: blocker, Type: LinkBlocks, To: n.ID}); err != nil {
			return err
		}
	}
	return nil
}

// GetIssue reads the issue with exactly this ID, retired or not; ok is false
// when there is none. A retired issue is outside the readiness graph, so it
// reads with its stored status, not blocked, and no parent.
func GetIssue(ctx context.Context, tx *sql.Tx, id string) (issue Issue, ok bool, err error) {
	issues, err := queryIssues(ctx, tx, `WHERE i.id = ?`, id)
	if err != nil {
		return Issue{}, false, fmt.Errorf("read issue %s: %w", id, err)
	}
	if len(issues) == 0 {
		return Issue{}, false, nil
	}
	return issues[0], true, nil
}

// queryIssues is the one read of issues as they display: the rows the tail
// (WHERE, ORDER BY, LIMIT) selects, each with its displayed status and
// blocked condition from issue_states, its labels, and its ancestors. The
// tail can refer to the issue as i, its state as s (NULL for a retired
// issue), and its description and notes bodies as d and n. The result is
// never nil.
func queryIssues(ctx context.Context, tx *sql.Tx, tail string, args ...any) ([]Issue, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT i.id, i.title, d.body, g.body, a.body, n.body, i.type, i.priority,
		        coalesce(s.display_status, i.status), i.status_reason, coalesce(s.blocked, 0),
		        i.assignee, i.owner, i.due_at, i.created_at, i.created_by, i.updated_at, i.retired_at
		 FROM issues i
		 JOIN texts d ON d.hash = i.description
		 JOIN texts g ON g.hash = i.design
		 JOIN texts a ON a.hash = i.acceptance_criteria
		 JOIN texts n ON n.hash = i.notes
		 LEFT JOIN issue_states s ON s.id = i.id
		 `+tail, args...)
	if err != nil {
		return nil, err
	}
	issues, err := scanIssues(rows)
	if err != nil || len(issues) == 0 {
		return issues, err
	}

	// Labels and ancestors for the whole result in two queries, not two per
	// issue.
	index := make(map[string]*Issue, len(issues))
	ids := make([]string, len(issues))
	for i := range issues {
		index[issues[i].ID] = &issues[i]
		ids[i] = issues[i].ID
	}
	idList, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	collect := func(query string, into func(*Issue) *[]string) error {
		rows, err := tx.QueryContext(ctx, query, string(idList))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, value string
			if err := rows.Scan(&id, &value); err != nil {
				return err
			}
			list := into(index[id])
			*list = append(*list, value)
		}
		return rows.Err()
	}
	if err := collect(
		`SELECT issue_id, label FROM labels
		 WHERE issue_id IN (SELECT value FROM json_each(?)) ORDER BY issue_id, label`,
		func(i *Issue) *[]string { return &i.Labels }); err != nil {
		return nil, fmt.Errorf("read labels: %w", err)
	}
	if err := collect(
		`SELECT id, ancestor_id FROM issue_ancestors
		 WHERE id IN (SELECT value FROM json_each(?)) ORDER BY id, depth`,
		func(i *Issue) *[]string { return &i.Ancestors }); err != nil {
		return nil, fmt.Errorf("read ancestors: %w", err)
	}
	for i := range issues {
		if len(issues[i].Ancestors) > 0 {
			issues[i].Parent = &issues[i].Ancestors[0]
		}
	}
	return issues, nil
}

func scanIssues(rows *sql.Rows) ([]Issue, error) {
	defer rows.Close()
	issues := []Issue{}
	for rows.Next() {
		issue := Issue{Labels: []string{}, Ancestors: []string{}}
		var reason, assignee, owner, due, retired sql.NullString
		var created, updated string
		err := rows.Scan(
			&issue.ID, &issue.Title, &issue.Description, &issue.Design, &issue.AcceptanceCriteria, &issue.Notes,
			&issue.Type, &issue.Priority, &issue.Status, &reason, &issue.Blocked,
			&assignee, &owner, &due, &created, &issue.CreatedBy, &updated, &retired)
		if err != nil {
			return nil, err
		}
		issue.StatusReason, issue.Assignee, issue.Owner = stringPtr(reason), stringPtr(assignee), stringPtr(owner)
		if issue.CreatedAt, err = ParseTime(created); err != nil {
			return nil, err
		}
		if issue.UpdatedAt, err = ParseTime(updated); err != nil {
			return nil, err
		}
		if issue.DueAt, err = timePtr(due); err != nil {
			return nil, err
		}
		if issue.RetiredAt, err = timePtr(retired); err != nil {
			return nil, err
		}
		issues = append(issues, issue)
	}
	return issues, rows.Err()
}

// LoadDetail fills in what `show` adds to an issue: its links, its comments,
// and, for a held issue, the status it had before the hold.
func LoadDetail(ctx context.Context, tx *sql.Tx, issue *Issue) error {
	links := &Links{
		Blocks: []string{}, BlockedBy: []string{}, Children: []string{},
		Related: []string{}, DiscoveredFrom: []string{}, Discovered: []string{},
	}
	byRole := map[string]*[]string{
		"blocks": &links.Blocks, "blocked_by": &links.BlockedBy, "children": &links.Children,
		"related": &links.Related, "discovered_from": &links.DiscoveredFrom, "discovered": &links.Discovered,
	}
	// The parent is on the issue itself, so only the parent's side of a
	// parent link is listed here. A related link reads the same from both ends.
	rows, err := tx.QueryContext(ctx,
		`SELECT l.role, l.other
		 FROM (
		     SELECT 'blocks' AS role, to_id AS other FROM links WHERE from_id = ?1 AND type = 'blocks'
		     UNION ALL SELECT 'blocked_by', from_id FROM links WHERE to_id = ?1 AND type = 'blocks'
		     UNION ALL SELECT 'children', to_id FROM links WHERE from_id = ?1 AND type = 'parent'
		     UNION ALL SELECT 'related', to_id FROM links WHERE from_id = ?1 AND type = 'related'
		     UNION ALL SELECT 'related', from_id FROM links WHERE to_id = ?1 AND type = 'related'
		     UNION ALL SELECT 'discovered_from', to_id FROM links WHERE from_id = ?1 AND type = 'discovered-from'
		     UNION ALL SELECT 'discovered', from_id FROM links WHERE to_id = ?1 AND type = 'discovered-from'
		 ) l
		 JOIN live_issues other ON other.id = l.other
		 ORDER BY l.other`, issue.ID)
	if err != nil {
		return fmt.Errorf("read links of %s: %w", issue.ID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var role, other string
		if err := rows.Scan(&role, &other); err != nil {
			return err
		}
		*byRole[role] = append(*byRole[role], other)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	issue.Links = links

	if issue.Comments, err = comments(ctx, tx, issue.ID); err != nil {
		return err
	}
	if issue.Status == "held" {
		if issue.PreviousStatus, err = previousStatus(ctx, tx, issue.ID); err != nil {
			return err
		}
	}
	return nil
}

func comments(ctx context.Context, tx *sql.Tx, issueID string) ([]Comment, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT id, author, text, created_at FROM comments WHERE issue_id = ? ORDER BY created_at, id`, issueID)
	if err != nil {
		return nil, fmt.Errorf("read comments of %s: %w", issueID, err)
	}
	defer rows.Close()
	out := []Comment{}
	for rows.Next() {
		var c Comment
		var at string
		if err := rows.Scan(&c.ID, &c.Author, &c.Text, &at); err != nil {
			return nil, err
		}
		if c.CreatedAt, err = ParseTime(at); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// InsertComment adds a comment to an issue and returns it as it reads back.
// A comment is its own record: it logs no event and leaves the issue's
// updated_at alone (ADR-0013). Its ID is a ULID that sorts after every
// comment already stored, so comments made in the same instant keep the
// order they were made in.
func InsertComment(ctx context.Context, tx *sql.Tx, issueID, author, text string, at time.Time) (Comment, error) {
	id, err := nextULID(ctx, tx, "comments", at)
	if err != nil {
		return Comment{}, err
	}
	stamp := FormatTime(at)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO comments (id, issue_id, author, text, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, issueID, author, text, stamp); err != nil {
		return Comment{}, fmt.Errorf("comment on %s: %w", issueID, err)
	}
	c := Comment{ID: id, Author: author, Text: text}
	c.CreatedAt, err = ParseTime(stamp)
	return c, err
}

// Patch is a typed change to an issue: nil fields are left alone. For the
// optional fields (StatusReason, Assignee, Owner) an empty string clears the
// value, and a zero DueAt or RetiredAt clears that timestamp. Status and
// RetiredAt are here so that every field change goes through the one diff;
// the rules about which status changes are allowed, and about retiring and
// restoring, belong to the tracker.
type Patch struct {
	Title              *string
	Type               *string
	Priority           *int
	Description        *string
	Design             *string
	AcceptanceCriteria *string
	Notes              *string
	Status             *string
	StatusReason       *string
	Assignee           *string
	Owner              *string
	DueAt              *time.Time
	RetiredAt          *time.Time
	AddLabels          []string
	RemoveLabels       []string
}

// UpdateIssue applies p to the issue and logs one event per attribute whose
// value actually changed, in the caller's transaction (ADR-0013). Text
// fields log content hashes (ADR-0014); labels log one element per event.
// updated_at moves to at when anything changed, and never has an event.
func UpdateIssue(ctx context.Context, tx *sql.Tx, id string, p Patch, at time.Time, by string) error {
	var cur struct {
		title, typ, status                     string
		priority                               int
		description, design, acceptance, notes string
		statusReason, assignee, owner          sql.NullString
		dueAtRaw, retiredAtRaw                 sql.NullString
	}
	err := tx.QueryRowContext(ctx,
		`SELECT title, type, priority, description, design, acceptance_criteria, notes,
		        status, status_reason, assignee, owner, due_at, retired_at
		 FROM issues WHERE id = ?`, id).Scan(
		&cur.title, &cur.typ, &cur.priority, &cur.description, &cur.design, &cur.acceptance, &cur.notes,
		&cur.status, &cur.statusReason, &cur.assignee, &cur.owner, &cur.dueAtRaw, &cur.retiredAtRaw)
	if err != nil {
		return fmt.Errorf("read issue %s: %w", id, err)
	}

	changed := false
	// set writes one column and its event. stored is the column's new value;
	// old and new are what the event shows (nil for an absent value).
	set := func(attr string, stored, old, new any) error {
		if _, err := tx.ExecContext(ctx, `UPDATE issues SET `+attr+` = ? WHERE id = ?`, stored, id); err != nil {
			return fmt.Errorf("update %s of %s: %w", attr, id, err)
		}
		changed = true
		return insertEvent(ctx, tx, id, at, by, attr, old, new)
	}
	required := func(attr, old string, new *string) error {
		if new == nil || *new == old {
			return nil
		}
		return set(attr, *new, old, *new)
	}
	optional := func(attr string, old sql.NullString, new *string) error {
		if new == nil || *new == old.String {
			return nil
		}
		return set(attr, nullable(*new), nullableAny(old.String), nullableAny(*new))
	}
	text := func(attr, oldHash string, body *string) error {
		if body == nil {
			return nil
		}
		hash, err := putText(ctx, tx, *body)
		if err != nil || hash == oldHash {
			return err
		}
		return set(attr, hash, oldHash, hash)
	}

	timestamp := func(attr string, oldRaw sql.NullString, new *time.Time) error {
		if new == nil {
			return nil
		}
		old, err := timePtr(oldRaw)
		if err != nil {
			return err
		}
		switch {
		case new.IsZero() && old == nil:
			return nil
		case new.IsZero():
			return set(attr, nil, eventTime(*old), nil)
		case old != nil && old.Equal(*new):
			return nil
		case old == nil:
			return set(attr, FormatTime(*new), nil, eventTime(*new))
		default:
			return set(attr, FormatTime(*new), eventTime(*old), eventTime(*new))
		}
	}

	steps := []func() error{
		func() error { return required("title", cur.title, p.Title) },
		func() error { return required("type", cur.typ, p.Type) },
		func() error {
			if p.Priority == nil || *p.Priority == cur.priority {
				return nil
			}
			return set("priority", *p.Priority, cur.priority, *p.Priority)
		},
		func() error { return text("description", cur.description, p.Description) },
		func() error { return text("design", cur.design, p.Design) },
		func() error { return text("acceptance_criteria", cur.acceptance, p.AcceptanceCriteria) },
		func() error { return text("notes", cur.notes, p.Notes) },
		func() error { return required("status", cur.status, p.Status) },
		func() error { return optional("status_reason", cur.statusReason, p.StatusReason) },
		func() error { return optional("assignee", cur.assignee, p.Assignee) },
		func() error { return optional("owner", cur.owner, p.Owner) },
		func() error { return timestamp("due_at", cur.dueAtRaw, p.DueAt) },
		func() error { return timestamp("retired_at", cur.retiredAtRaw, p.RetiredAt) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}

	for _, label := range p.AddLabels {
		res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO labels (issue_id, label) VALUES (?, ?)`, id, label)
		if err != nil {
			return fmt.Errorf("add label to %s: %w", id, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			changed = true
			if err := insertEvent(ctx, tx, id, at, by, "labels", nil, label); err != nil {
				return err
			}
		}
	}
	for _, label := range p.RemoveLabels {
		res, err := tx.ExecContext(ctx, `DELETE FROM labels WHERE issue_id = ? AND label = ?`, id, label)
		if err != nil {
			return fmt.Errorf("remove label from %s: %w", id, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			changed = true
			if err := insertEvent(ctx, tx, id, at, by, "labels", label, nil); err != nil {
				return err
			}
		}
	}

	if changed {
		if _, err := tx.ExecContext(ctx, `UPDATE issues SET updated_at = ? WHERE id = ?`, FormatTime(at), id); err != nil {
			return fmt.Errorf("update %s: %w", id, err)
		}
	}
	return nil
}

// putText stores body in the write-once texts table and returns its hash.
func putText(ctx context.Context, tx *sql.Tx, body string) (string, error) {
	sum := sha256.Sum256([]byte(body))
	hash := hex.EncodeToString(sum[:])
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO texts (hash, body) VALUES (?, ?)`, hash, body); err != nil {
		return "", fmt.Errorf("store text: %w", err)
	}
	return hash, nil
}

// queryStrings returns a one-column result as a slice that is never nil.
func queryStrings(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]string, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// nullable is s as a column value, the empty string being NULL.
func nullable(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

// nullableTime is t as a column value, the zero time being NULL.
func nullableTime(t time.Time) sql.NullString {
	if t.IsZero() {
		return sql.NullString{}
	}
	return sql.NullString{String: FormatTime(t), Valid: true}
}

// nullableAny is s as an event value, the empty string being null.
func nullableAny(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func stringPtr(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	return &s.String
}

func timePtr(s sql.NullString) (*time.Time, error) {
	if !s.Valid {
		return nil, nil
	}
	t, err := ParseTime(s.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
