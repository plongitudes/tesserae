package sqlite

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

// Event is one recorded change to one attribute of one issue (ADR-0013).
// OldValue and NewValue are nil for an absent value, a string, or an int.
type Event struct {
	ID        string    `json:"id"`
	ChangedAt time.Time `json:"changed_at"`
	ChangedBy string    `json:"changed_by"`
	Attr      string    `json:"attr"`
	OldValue  any       `json:"old_value"`
	NewValue  any       `json:"new_value"`
}

// LoadHistory fills in the issue's events, oldest first.
func LoadHistory(ctx context.Context, tx *sql.Tx, issue *Issue) error {
	rows, err := tx.QueryContext(ctx,
		`SELECT id, changed_at, changed_by, attr, old_value, new_value
		 FROM events WHERE issue_id = ? ORDER BY id`, issue.ID)
	if err != nil {
		return fmt.Errorf("read events of %s: %w", issue.ID, err)
	}
	defer rows.Close()
	history := []Event{}
	for rows.Next() {
		var e Event
		var at string
		var old, new sql.NullString
		if err := rows.Scan(&e.ID, &at, &e.ChangedBy, &e.Attr, &old, &new); err != nil {
			return err
		}
		if e.ChangedAt, err = ParseTime(at); err != nil {
			return err
		}
		if e.OldValue, err = decodeEventValue(old); err != nil {
			return err
		}
		if e.NewValue, err = decodeEventValue(new); err != nil {
			return err
		}
		history = append(history, e)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	issue.History = history
	return nil
}

// previousStatus is the status a held issue had before the hold: the old
// value of its latest status event, or nil if no event recorded the hold
// (an issue imported as held).
func previousStatus(ctx context.Context, tx *sql.Tx, issueID string) (*string, error) {
	var old, new sql.NullString
	err := tx.QueryRowContext(ctx,
		`SELECT old_value, new_value FROM events
		 WHERE issue_id = ? AND attr = 'status' ORDER BY id DESC LIMIT 1`, issueID).Scan(&old, &new)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the status events of %s: %w", issueID, err)
	}
	if to, err := decodeEventValue(new); err != nil || to != "held" {
		return nil, err
	}
	from, err := decodeEventValue(old)
	if s, ok := from.(string); ok && err == nil {
		return &s, nil
	}
	return nil, err
}

// insertEvent appends one event. old and new are nil, a string, or an int;
// they are stored as JSON, with SQL NULL for nil. Unexported on purpose:
// events are written only here in the store, by the functions that make the
// change.
func insertEvent(ctx context.Context, tx *sql.Tx, issueID string, at time.Time, by, attr string, old, new any) error {
	id, err := nextEventID(ctx, tx, at)
	if err != nil {
		return err
	}
	oldJSON, err := encodeEventValue(old)
	if err != nil {
		return err
	}
	newJSON, err := encodeEventValue(new)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO events (id, issue_id, changed_at, changed_by, attr, old_value, new_value)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`, id, issueID, FormatTime(at), by, attr, oldJSON, newJSON)
	if err != nil {
		return fmt.Errorf("log %s event for %s: %w", attr, issueID, err)
	}
	return nil
}

// nextEventID returns a ULID for an event at the given time that sorts after
// every event already in the store, so that ID order is the order in which
// changes were made even when the clock stands still or steps back. A fresh
// ULID is used when it already sorts last; otherwise the latest ID plus one.
func nextEventID(ctx context.Context, tx *sql.Tx, at time.Time) (string, error) {
	id, err := ulid.New(ulid.Timestamp(at), rand.Reader)
	if err != nil {
		return "", fmt.Errorf("new event ID: %w", err)
	}
	var latest sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT max(id) FROM events`).Scan(&latest); err != nil {
		return "", fmt.Errorf("read the latest event ID: %w", err)
	}
	if latest.Valid {
		// An ID that isn't a ULID can't be stepped past; the fresh one stands.
		if prev, err := ulid.ParseStrict(latest.String); err == nil && id.Compare(prev) <= 0 {
			id = prev
			for i := len(id) - 1; i >= 0; i-- {
				id[i]++
				if id[i] != 0 {
					break
				}
			}
		}
	}
	return id.String(), nil
}

// eventTime is a timestamp as an event value: RFC 3339 UTC, as in output.
func eventTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func encodeEventValue(v any) (sql.NullString, error) {
	if v == nil {
		return sql.NullString{}, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return sql.NullString{}, fmt.Errorf("encode event value: %w", err)
	}
	return sql.NullString{String: string(b), Valid: true}, nil
}

func decodeEventValue(s sql.NullString) (any, error) {
	if !s.Valid {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(s.String)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("decode event value %q: %w", s.String, err)
	}
	if n, ok := v.(json.Number); ok {
		if i, err := n.Int64(); err == nil {
			return int(i), nil
		}
	}
	return v, nil
}
