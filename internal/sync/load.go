package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"time"

	"github.com/plongitudes/tesserae/internal/store/sqlite"
	"github.com/plongitudes/tesserae/internal/tracker"
)

// Load reads a database as a snapshot: every issue, retired or not, with
// its stored status and the links as they are stored, and every comment,
// event and text. Timestamps are cut to the second, as a snapshot holds
// them. A database that `tess init` has not set up is refused with
// not_initialized, and Load never creates one.
func Load(ctx context.Context, dbPath string) (*Snapshot, error) {
	if _, err := os.Stat(dbPath); errors.Is(err, fs.ErrNotExist) {
		return nil, notInitialized()
	} else if err != nil {
		return nil, err
	}
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return nil, err
	}
	defer store.Close()

	var snap *Snapshot
	// One read transaction, so the snapshot is the database at one moment.
	err = store.Read(ctx, func(tx *sql.Tx) error {
		snap, err = loadIn(ctx, tx)
		return err
	})
	if err != nil {
		return nil, err
	}
	return snap, nil
}

func notInitialized() *tracker.Error {
	return &tracker.Error{
		Code:    tracker.CodeNotInitialized,
		Message: "this repository has no tess database yet; run `tess init` to create one",
		Hint:    "run `tess init`",
	}
}

func loadIn(ctx context.Context, tx *sql.Tx) (*Snapshot, error) {
	prefix, ok, err := sqlite.ConfigIn(ctx, tx, "prefix")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, notInitialized()
	}
	s := &Snapshot{
		Config:   Config{Format: Format, Prefix: prefix},
		Issues:   map[string]*Issue{},
		Comments: map[string]*Comment{},
		Events:   map[string]*Event{},
		Texts:    map[string]string{},
	}
	for _, step := range []func(context.Context, *sql.Tx) error{
		s.loadIssues, s.loadLabels, s.loadLinks, s.loadComments, s.loadEvents, s.loadTexts,
	} {
		if err := step(ctx, tx); err != nil {
			return nil, err
		}
	}
	for _, i := range s.Issues {
		slices.Sort(i.BlockedBy)
		slices.Sort(i.Related)
		slices.Sort(i.DiscoveredFrom)
	}
	return s, nil
}

// each runs a query and calls scan for every row.
func each(ctx context.Context, tx *sql.Tx, what, query string, scan func(*sql.Rows) error) error {
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("read %s: %w", what, err)
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return fmt.Errorf("read %s: %w", what, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read %s: %w", what, err)
	}
	return nil
}

func (s *Snapshot) loadIssues(ctx context.Context, tx *sql.Tx) error {
	// The issues table itself, not live_issues or issue_states: retired
	// issues travel, and the status is the stored one.
	return each(ctx, tx, "issues",
		`SELECT id, title, description, design, acceptance_criteria, notes, type, priority,
		        status, status_reason, assignee, owner, due_at, created_at, created_by, updated_at, retired_at
		 FROM issues`,
		func(rows *sql.Rows) error {
			i := &Issue{Labels: []string{}, BlockedBy: []string{}, Related: []string{}, DiscoveredFrom: []string{}}
			var reason, assignee, owner, due, retired sql.NullString
			var created, updated string
			err := rows.Scan(&i.ID, &i.Title, &i.Description, &i.Design, &i.AcceptanceCriteria, &i.Notes,
				&i.Type, &i.Priority, &i.Status, &reason, &assignee, &owner, &due,
				&created, &i.CreatedBy, &updated, &retired)
			if err != nil {
				return err
			}
			i.StatusReason, i.Assignee, i.Owner = stringPtr(reason), stringPtr(assignee), stringPtr(owner)
			if i.CreatedAt, err = storedTime(created); err != nil {
				return err
			}
			if i.UpdatedAt, err = storedTime(updated); err != nil {
				return err
			}
			if i.DueAt, err = storedTimePtr(due); err != nil {
				return err
			}
			if i.RetiredAt, err = storedTimePtr(retired); err != nil {
				return err
			}
			s.Issues[i.ID] = i
			return nil
		})
}

func (s *Snapshot) loadLabels(ctx context.Context, tx *sql.Tx) error {
	return each(ctx, tx, "labels", `SELECT issue_id, label FROM labels ORDER BY issue_id, label`,
		func(rows *sql.Rows) error {
			var id, label string
			if err := rows.Scan(&id, &label); err != nil {
				return err
			}
			i, err := s.issue(id)
			if err != nil {
				return err
			}
			i.Labels = append(i.Labels, label)
			return nil
		})
}

// loadLinks puts each link on the issue whose event log records it
// (sqlite.LinkOwner), except related, which has no direction and goes on
// the smaller ID so that both wordings give the same file.
func (s *Snapshot) loadLinks(ctx context.Context, tx *sql.Tx) error {
	return each(ctx, tx, "links", `SELECT from_id, type, to_id FROM links`,
		func(rows *sql.Rows) error {
			var l sqlite.Link
			if err := rows.Scan(&l.From, &l.Type, &l.To); err != nil {
				return err
			}
			if l.Type == sqlite.LinkRelated && l.From > l.To {
				l.From, l.To = l.To, l.From
			}
			ownerID := sqlite.LinkOwner(l)
			other := l.From
			if other == ownerID {
				other = l.To
			}
			owner, err := s.issue(ownerID)
			if err != nil {
				return err
			}
			switch l.Type {
			case sqlite.LinkBlocks:
				owner.BlockedBy = append(owner.BlockedBy, other)
			case sqlite.LinkParent:
				owner.Parent = &other
			case sqlite.LinkDiscoveredFrom:
				owner.DiscoveredFrom = append(owner.DiscoveredFrom, other)
			case sqlite.LinkRelated:
				owner.Related = append(owner.Related, other)
			default:
				return fmt.Errorf("link %s %s %s has a type sync does not know", l.From, l.Type, l.To)
			}
			return nil
		})
}

func (s *Snapshot) loadComments(ctx context.Context, tx *sql.Tx) error {
	return each(ctx, tx, "comments", `SELECT id, issue_id, author, text, created_at FROM comments`,
		func(rows *sql.Rows) error {
			c := &Comment{}
			var created string
			err := rows.Scan(&c.ID, &c.IssueID, &c.Author, &c.Text, &created)
			if err != nil {
				return err
			}
			if c.CreatedAt, err = storedTime(created); err != nil {
				return err
			}
			s.Comments[c.ID] = c
			return nil
		})
}

func (s *Snapshot) loadEvents(ctx context.Context, tx *sql.Tx) error {
	return each(ctx, tx, "events",
		`SELECT id, issue_id, changed_at, changed_by, attr, old_value, new_value FROM events`,
		func(rows *sql.Rows) error {
			e := &Event{}
			var changed string
			var old, new sql.NullString
			err := rows.Scan(&e.ID, &e.IssueID, &changed, &e.ChangedBy, &e.Attr, &old, &new)
			if err != nil {
				return err
			}
			if e.ChangedAt, err = storedTime(changed); err != nil {
				return err
			}
			if e.OldValue, err = eventValue(old); err != nil {
				return err
			}
			if e.NewValue, err = eventValue(new); err != nil {
				return err
			}
			s.Events[e.ID] = e
			return nil
		})
}

func (s *Snapshot) loadTexts(ctx context.Context, tx *sql.Tx) error {
	return each(ctx, tx, "texts", `SELECT hash, body FROM texts`,
		func(rows *sql.Rows) error {
			var hash, body string
			if err := rows.Scan(&hash, &body); err != nil {
				return err
			}
			s.Texts[hash] = body
			return nil
		})
}

func (s *Snapshot) issue(id string) (*Issue, error) {
	i, ok := s.Issues[id]
	if !ok {
		return nil, fmt.Errorf("a row refers to issue %s, which is not in the database", id)
	}
	return i, nil
}

// eventValue is a stored event value (JSON text, SQL NULL for null) as the
// JSON value a snapshot holds: nil, a string, or a float64, the same as
// FromFiles gives.
func eventValue(s sql.NullString) (any, error) {
	if !s.Valid {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal([]byte(s.String), &v); err != nil {
		return nil, fmt.Errorf("event value %q: %w", s.String, err)
	}
	return v, nil
}

func storedTime(s string) (time.Time, error) {
	t, err := sqlite.ParseTime(s)
	if err != nil {
		return time.Time{}, err
	}
	return t.Truncate(time.Second), nil
}

func storedTimePtr(s sql.NullString) (*time.Time, error) {
	if !s.Valid {
		return nil, nil
	}
	t, err := storedTime(s.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func stringPtr(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	return &s.String
}
