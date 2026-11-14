package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// The facts a status change is judged on. Each is a read of the readiness
// views (ADR-0007); the rules about what they allow belong to the tracker.

// StoredStatus returns the status an issue holds, which for a not-started
// issue is ready even while it displays as waiting.
func StoredStatus(ctx context.Context, tx *sql.Tx, id string) (string, error) {
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM issues WHERE id = ?`, id).Scan(&status); err != nil {
		return "", fmt.Errorf("read status of %s: %w", id, err)
	}
	return status, nil
}

// IsBlocked reports whether the issue is in blocked_issues.
func IsBlocked(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var blocked bool
	err := tx.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM blocked_issues WHERE id = ?)`, id).Scan(&blocked)
	return blocked, err
}

// OpenChildren returns the issue's open children, sorted: its descendants
// that aren't done, omit, or retired (ADR-0016).
func OpenChildren(ctx context.Context, tx *sql.Tx, id string) ([]string, error) {
	return queryStrings(ctx, tx, `SELECT id FROM open_children WHERE parent_id = ? ORDER BY id`, id)
}

// BlockedIssues returns the ID of every blocked issue. Take it before a
// change and give it to NewlyUnblocked after, in the same transaction.
func BlockedIssues(ctx context.Context, tx *sql.Tx) ([]string, error) {
	return queryStrings(ctx, tx, `SELECT id FROM blocked_issues`)
}

// NewlyUnblocked returns the issues that were blocked before a change and
// aren't now: blockedBefore minus blocked_issues, in list order (priority,
// created_at, id). An issue that left the set by being retired isn't
// unblocked and is left out.
func NewlyUnblocked(ctx context.Context, tx *sql.Tx, blockedBefore []string) ([]string, error) {
	if len(blockedBefore) == 0 {
		return []string{}, nil
	}
	before, err := json.Marshal(blockedBefore)
	if err != nil {
		return nil, err
	}
	return queryStrings(ctx, tx,
		`SELECT i.id
		 FROM live_issues i
		 WHERE i.id IN (SELECT value FROM json_each(?))
		   AND i.id NOT IN (SELECT id FROM blocked_issues)
		 ORDER BY i.priority, i.created_at, i.id`, string(before))
}

// ActiveBlockers returns, for every issue that has them, its active
// blockers in list order, read from the view blocked_issues is built on.
func ActiveBlockers(ctx context.Context, tx *sql.Tx) (map[string][]string, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT b.id, b.blocker_id
		 FROM active_blockers b
		 JOIN live_issues i ON i.id = b.blocker_id
		 ORDER BY i.priority, i.created_at, i.id`)
	if err != nil {
		return nil, fmt.Errorf("read active blockers: %w", err)
	}
	defer rows.Close()
	blockers := map[string][]string{}
	for rows.Next() {
		var id, blocker string
		if err := rows.Scan(&id, &blocker); err != nil {
			return nil, err
		}
		blockers[id] = append(blockers[id], blocker)
	}
	return blockers, rows.Err()
}
