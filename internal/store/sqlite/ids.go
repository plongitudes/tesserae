package sqlite

import (
	"context"
	"database/sql"
)

// The facts the ids package needs from the store. The rules themselves
// (which candidate a partial ID means, how long a new ID is, what to do on a
// collision) live in internal/ids; the tracker joins the two.

// IDCandidate is an issue whose ID contains some typed text.
type IDCandidate struct {
	ID      string
	Retired bool
}

// IDCandidates returns the issues a typed ID could mean: the issue with
// exactly that ID if there is one, otherwise every issue whose ID contains
// the input, compared literally and case-sensitively. Retired issues are
// included and marked, because an exact ID reaches a retired issue and a
// partial one must not (ADR-0010). So this reads the issues table, and takes
// "retired" from live_issues, where the filter is defined.
func IDCandidates(ctx context.Context, tx *sql.Tx, input string) ([]IDCandidate, error) {
	if input == "" {
		return nil, nil
	}
	// The exact ID is a primary-key lookup; only a partial ID pays for a scan
	// of the key.
	rows, err := tx.QueryContext(ctx, `SELECT i.id, `+isRetired+` FROM issues i WHERE i.id = ?`, input)
	if err != nil {
		return nil, err
	}
	exact, err := scanIDCandidates(rows)
	if err != nil || len(exact) > 0 {
		return exact, err
	}
	rows, err = tx.QueryContext(ctx,
		`SELECT i.id, `+isRetired+` FROM issues i WHERE instr(i.id, ?) > 0 ORDER BY i.id`, input)
	if err != nil {
		return nil, err
	}
	return scanIDCandidates(rows)
}

// isRetired is whether the issue i is retired: not among live_issues.
const isRetired = `NOT EXISTS (SELECT 1 FROM live_issues live WHERE live.id = i.id)`

func scanIDCandidates(rows *sql.Rows) ([]IDCandidate, error) {
	defer rows.Close()
	var out []IDCandidate
	for rows.Next() {
		var c IDCandidate
		if err := rows.Scan(&c.ID, &c.Retired); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// IssueCount returns how many issues the store holds, retired ones included:
// the number a new ID's length adapts to.
func IssueCount(ctx context.Context, tx *sql.Tx) (int, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM issues`).Scan(&n)
	return n, err
}

// IssueExists reports whether id is taken by any issue, retired or not.
func IssueExists(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var exists bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM issues WHERE id = ?)`, id).Scan(&exists)
	return exists, err
}
