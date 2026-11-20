package sync

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/plongitudes/tesserae/internal/store/sqlite"
)

// The config keys that record a copy's last sync. They are per copy and
// never travel (sync.md, "The snapshot").
const (
	// ConfigLastAt is the time of the last sync that succeeded, RFC 3339
	// UTC to the second.
	ConfigLastAt = "sync.last_at"
	// ConfigLastResult is ResultOK, or the error code of the last sync if
	// it failed.
	ConfigLastResult = "sync.last_result"
	ResultOK         = "ok"
)

// ErrChangedDuringSync is Store refusing to write over an issue that a
// command changed after the snapshot was loaded. Nothing was written and
// the local ref has not moved, so the next sync merges the same remote
// state with the changed issue.
var ErrChangedDuringSync = errors.New("an issue changed while the sync ran")

// Store writes a merged snapshot into the database, in one transaction
// (sync.md, "Writing the merged snapshot"). ours is the snapshot the merge
// was given as this copy's state. For every issue the merge changed from
// ours, the row, labels and links are replaced; comments, events and texts
// the database lacks are added. No event is logged: the events arrive with
// the snapshot. Everything else is left alone, including what a command
// wrote after ours was loaded; if such a command changed an issue the merge
// also changed, nothing is written and the error is ErrChangedDuringSync.
//
// at is the sync's time, recorded with the result as config.
func Store(ctx context.Context, dbPath string, ours, merged *Snapshot, at time.Time) error {
	store, err := openExisting(ctx, dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	return store.Write(ctx, func(tx *sql.Tx) error {
		now, err := loadIn(ctx, tx)
		if err != nil {
			return err
		}
		w := &writer{ctx: ctx, tx: tx, now: now, merged: merged}
		if err := w.findChanged(ours); err != nil {
			return err
		}
		for _, step := range []func() error{w.texts, w.issues, w.labels, w.links, w.dropReplaced, w.comments, w.events} {
			if err := step(); err != nil {
				return err
			}
		}
		if err := sqlite.SetConfig(ctx, tx, ConfigLastAt, formatTime(at)); err != nil {
			return err
		}
		return sqlite.SetConfig(ctx, tx, ConfigLastResult, ResultOK)
	})
}

// RecordFailure records the code a sync failed with. The time of the last
// sync that succeeded stays.
func RecordFailure(ctx context.Context, dbPath, code string) error {
	store, err := openExisting(ctx, dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	return store.Write(ctx, func(tx *sql.Tx) error {
		if _, ok, err := sqlite.ConfigIn(ctx, tx, "prefix"); err != nil {
			return err
		} else if !ok {
			return notInitialized()
		}
		return sqlite.SetConfig(ctx, tx, ConfigLastResult, code)
	})
}

// writer is one write of a merged snapshot, inside its transaction.
type writer struct {
	ctx    context.Context
	tx     *sql.Tx
	now    *Snapshot // the database as the transaction found it
	merged *Snapshot
	// The issues the merge changed, in ID order. For each, the database
	// holds the record the merge was given, or none.
	changed []string
}

// findChanged lists the issues whose merged record is not the one the merge
// was given, and refuses if the database no longer holds that one.
func (w *writer) findChanged(ours *Snapshot) error {
	for _, id := range slices.Sorted(maps.Keys(w.merged.Issues)) {
		given := recordOf(ours.Issues[id])
		if bytes.Equal(recordOf(w.merged.Issues[id]), given) {
			continue
		}
		if !bytes.Equal(recordOf(w.now.Issues[id]), given) {
			return fmt.Errorf("%w: %s", ErrChangedDuringSync, id)
		}
		w.changed = append(w.changed, id)
	}
	return nil
}

// recordOf is an issue's file, or nothing for no issue.
func recordOf(i *Issue) []byte {
	if i == nil {
		return nil
	}
	return issueBytes(i)
}

func (w *writer) exec(what, query string, args ...any) error {
	if _, err := w.tx.ExecContext(w.ctx, query, args...); err != nil {
		return fmt.Errorf("sync: write %s: %w", what, err)
	}
	return nil
}

// texts adds the bodies the database lacks. They go first: an issue row
// refers to its texts.
func (w *writer) texts() error {
	for _, hash := range slices.Sorted(maps.Keys(w.merged.Texts)) {
		if _, ok := w.now.Texts[hash]; ok {
			continue
		}
		err := w.exec("text "+hash, `INSERT INTO texts (hash, body) VALUES (?, ?)`, hash, w.merged.Texts[hash])
		if err != nil {
			return err
		}
	}
	return nil
}

const insertIssue = `
INSERT INTO issues (id, title, description, design, acceptance_criteria, notes, type, priority,
                    status, status_reason, assignee, owner, created_by,
                    due_at, created_at, updated_at, retired_at)
VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12, ?13, ?14, ?15, ?16, ?17)`

// updateIssue replaces a row. A snapshot's timestamps are to the second and
// the database's to the millisecond, so a timestamp the merge did not move
// keeps the stored text: `list` orders by created_at.
const updateIssue = `
UPDATE issues SET
    title = ?2, description = ?3, design = ?4, acceptance_criteria = ?5, notes = ?6, type = ?7, priority = ?8,
    status = ?9, status_reason = ?10, assignee = ?11, owner = ?12, created_by = ?13,
    due_at     = CASE WHEN substr(due_at, 1, 19)     = substr(?14, 1, 19) THEN due_at     ELSE ?14 END,
    created_at = CASE WHEN substr(created_at, 1, 19) = substr(?15, 1, 19) THEN created_at ELSE ?15 END,
    updated_at = CASE WHEN substr(updated_at, 1, 19) = substr(?16, 1, 19) THEN updated_at ELSE ?16 END,
    retired_at = CASE WHEN substr(retired_at, 1, 19) = substr(?17, 1, 19) THEN retired_at ELSE ?17 END
WHERE id = ?1`

// issues writes the row of every changed issue. All rows go before any
// link, comment or event, which refer to them.
func (w *writer) issues() error {
	for _, id := range w.changed {
		i := w.merged.Issues[id]
		query := insertIssue
		if w.now.Issues[id] != nil {
			query = updateIssue
		}
		err := w.exec("issue "+id, query,
			i.ID, i.Title, i.Description, i.Design, i.AcceptanceCriteria, i.Notes, i.Type, i.Priority,
			i.Status, i.StatusReason, i.Assignee, i.Owner, i.CreatedBy,
			storedTimeOf(i.DueAt), sqlite.FormatTime(i.CreatedAt), sqlite.FormatTime(i.UpdatedAt), storedTimeOf(i.RetiredAt))
		if err != nil {
			return err
		}
	}
	return nil
}

func storedTimeOf(t *time.Time) any {
	if t == nil {
		return nil
	}
	return sqlite.FormatTime(*t)
}

// held is the stored record of a changed issue, or an empty one for an
// issue that is new here.
func (w *writer) held(id string) *Issue {
	if i := w.now.Issues[id]; i != nil {
		return i
	}
	return &Issue{ID: id}
}

func (w *writer) labels() error {
	for _, id := range w.changed {
		was, is := w.held(id).Labels, w.merged.Issues[id].Labels
		for _, label := range was {
			if slices.Contains(is, label) {
				continue
			}
			if err := w.exec("labels of "+id, `DELETE FROM labels WHERE issue_id = ? AND label = ?`, id, label); err != nil {
				return err
			}
		}
		for _, label := range is {
			if slices.Contains(was, label) {
				continue
			}
			if err := w.exec("labels of "+id, `INSERT INTO labels (issue_id, label) VALUES (?, ?)`, id, label); err != nil {
				return err
			}
		}
	}
	return nil
}

// linksOf is the links an issue's record holds, each in the direction the
// store keeps it (ADR-0015); related, which has none, from the issue.
func linksOf(i *Issue) []sqlite.Link {
	var out []sqlite.Link
	if i.Parent != nil {
		out = append(out, sqlite.Link{From: *i.Parent, Type: sqlite.LinkParent, To: i.ID})
	}
	for _, other := range i.BlockedBy {
		out = append(out, sqlite.Link{From: other, Type: sqlite.LinkBlocks, To: i.ID})
	}
	for _, other := range i.DiscoveredFrom {
		out = append(out, sqlite.Link{From: i.ID, Type: sqlite.LinkDiscoveredFrom, To: other})
	}
	for _, other := range i.Related {
		out = append(out, sqlite.Link{From: i.ID, Type: sqlite.LinkRelated, To: other})
	}
	return out
}

// links replaces the links each changed issue holds: the rows that went are
// deleted and the new ones inserted, and a link in both is not touched. An
// issue's deletes come before its inserts, so that a child moving to
// another parent never has two.
func (w *writer) links() error {
	for _, id := range w.changed {
		was, is := linksOf(w.held(id)), linksOf(w.merged.Issues[id])
		for _, l := range was {
			if slices.Contains(is, l) {
				continue
			}
			// A related row is stored as it was given, either way round.
			err := w.exec(fmt.Sprintf("link %s %s %s", l.From, l.Type, l.To),
				`DELETE FROM links
				 WHERE type = ?2 AND ((from_id = ?1 AND to_id = ?3)
				                   OR (?2 = 'related' AND from_id = ?3 AND to_id = ?1))`, l.From, l.Type, l.To)
			if err != nil {
				return err
			}
		}
		for _, l := range is {
			if slices.Contains(was, l) {
				continue
			}
			if err := sqlite.InsertLinkUnlogged(w.ctx, w.tx, l); err != nil {
				return fmt.Errorf("sync: write %w", err)
			}
		}
	}
	return nil
}

// dropReplaced deletes the comments and events of a record the merge
// dropped whole: a retired issue whose ID a different issue has (ADR-0023).
// It is the one case in which the write deletes a record; such an issue is
// known by its created_at or created_by, which otherwise never change.
func (w *writer) dropReplaced() error {
	replaced := map[string]bool{}
	for _, id := range w.changed {
		was, is := w.now.Issues[id], w.merged.Issues[id]
		if was != nil && (!was.CreatedAt.Equal(is.CreatedAt) || was.CreatedBy != is.CreatedBy) {
			replaced[id] = true
		}
	}
	if len(replaced) == 0 {
		return nil
	}
	for _, id := range slices.Sorted(maps.Keys(w.now.Comments)) {
		if _, kept := w.merged.Comments[id]; kept || !replaced[w.now.Comments[id].IssueID] {
			continue
		}
		if err := w.exec("comment "+id, `DELETE FROM comments WHERE id = ?`, id); err != nil {
			return err
		}
		delete(w.now.Comments, id)
	}
	for _, id := range slices.Sorted(maps.Keys(w.now.Events)) {
		if _, kept := w.merged.Events[id]; kept || !replaced[w.now.Events[id].IssueID] {
			continue
		}
		if err := w.exec("event "+id, `DELETE FROM events WHERE id = ?`, id); err != nil {
			return err
		}
		delete(w.now.Events, id)
	}
	return nil
}

func (w *writer) comments() error {
	for _, id := range slices.Sorted(maps.Keys(w.merged.Comments)) {
		if _, ok := w.now.Comments[id]; ok {
			continue
		}
		c := w.merged.Comments[id]
		err := w.exec("comment "+id, `INSERT INTO comments (id, issue_id, author, text, created_at) VALUES (?, ?, ?, ?, ?)`,
			c.ID, c.IssueID, c.Author, c.Text, sqlite.FormatTime(c.CreatedAt))
		if err != nil {
			return err
		}
	}
	return nil
}

// events adds the events the database lacks, with the IDs they were minted
// with. This is the only place an event row is written outside the store's
// own changes, and it writes what another copy's store logged.
func (w *writer) events() error {
	for _, id := range slices.Sorted(maps.Keys(w.merged.Events)) {
		if _, ok := w.now.Events[id]; ok {
			continue
		}
		e := w.merged.Events[id]
		old, err := storedEventValue(e.OldValue)
		if err != nil {
			return err
		}
		new, err := storedEventValue(e.NewValue)
		if err != nil {
			return err
		}
		err = w.exec("event "+id,
			`INSERT INTO events (id, issue_id, changed_at, changed_by, attr, old_value, new_value)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			e.ID, e.IssueID, sqlite.FormatTime(e.ChangedAt), e.ChangedBy, e.Attr, old, new)
		if err != nil {
			return err
		}
	}
	return nil
}

// storedEventValue is the inverse of eventValue: JSON text, SQL NULL for
// null.
func storedEventValue(v any) (sql.NullString, error) {
	if v == nil {
		return sql.NullString{}, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return sql.NullString{}, fmt.Errorf("sync: event value %v: %w", v, err)
	}
	return sql.NullString{String: string(b), Valid: true}, nil
}
