package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// The four link types (ADR-0015).
const (
	LinkBlocks         = "blocks"
	LinkParent         = "parent"
	LinkRelated        = "related"
	LinkDiscoveredFrom = "discovered-from"
)

// Link is one link in its canonical direction: for blocks, From is the
// blocker; for parent, From is the parent; for discovered-from, From is the
// issue that was discovered. A related link has no direction and is held the
// way round it was first given.
type Link struct {
	From string `json:"from"`
	Type string `json:"type"`
	To   string `json:"to"`
}

// linkEvent says where a link is logged: on which end's history, and under
// which attribute. The value logged is the other end's ID. Links are logged
// once, on the dependent or child side (acceptance/README.md, "Event
// attribute names").
func linkEvent(l Link) (issueID, attr, other string) {
	switch l.Type {
	case LinkBlocks:
		return l.To, "blocked_by", l.From
	case LinkParent:
		return l.To, "parent", l.From
	case LinkDiscoveredFrom:
		return l.From, "discovered_from", l.To
	default:
		return l.From, "related", l.To
	}
}

// LinkOwner returns the issue a link is logged on, which is the issue that
// adding or removing the link changes.
func LinkOwner(l Link) string {
	id, _, _ := linkEvent(l)
	return id
}

// FindLink returns the link as it is stored; ok is false when there is none.
// A related link is found whichever way round it is asked for.
func FindLink(ctx context.Context, tx *sql.Tx, l Link) (stored Link, ok bool, err error) {
	query := `SELECT from_id, type, to_id FROM links WHERE from_id = ?1 AND type = ?2 AND to_id = ?3`
	if l.Type == LinkRelated {
		query = `SELECT from_id, type, to_id FROM links
		         WHERE type = ?2 AND ((from_id = ?1 AND to_id = ?3) OR (from_id = ?3 AND to_id = ?1))`
	}
	err = tx.QueryRowContext(ctx, query, l.From, l.Type, l.To).Scan(&stored.From, &stored.Type, &stored.To)
	if errors.Is(err, sql.ErrNoRows) {
		return Link{}, false, nil
	}
	if err != nil {
		return Link{}, false, fmt.Errorf("read link %s %s %s: %w", l.From, l.Type, l.To, err)
	}
	return stored, true, nil
}

// ParentOf returns the parent an issue is linked to, retired or not; ok is
// false when it has none. (An issue reads with no parent while its parent is
// retired, but the link is still there and comes back on restore.)
func ParentOf(ctx context.Context, tx *sql.Tx, child string) (parent string, ok bool, err error) {
	err = tx.QueryRowContext(ctx,
		`SELECT from_id FROM links WHERE to_id = ? AND type = 'parent'`, child).Scan(&parent)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read the parent of %s: %w", child, err)
	}
	return parent, true, nil
}

// ClosesLoop reports whether a readiness-affecting dependency from one issue
// to another would close a loop: whether `to` already leads back to `from`
// along blocks and parent links, taken together. A link from an issue to
// itself is a loop. It walks readiness_links, so a path through a retired
// issue counts.
func ClosesLoop(ctx context.Context, tx *sql.Tx, from, to string) (bool, error) {
	var loop bool
	// UNION keeps each issue once, so the walk ends even on a damaged
	// database that already holds a loop.
	err := tx.QueryRowContext(ctx,
		`WITH RECURSIVE reach (id) AS (
		     SELECT ?2
		     UNION
		     SELECT l.to_id FROM reach JOIN readiness_links l ON l.from_id = reach.id
		 )
		 SELECT EXISTS (SELECT 1 FROM reach WHERE id = ?1)`, from, to).Scan(&loop)
	if err != nil {
		return false, fmt.Errorf("check %s -> %s for a loop: %w", from, to, err)
	}
	return loop, nil
}

// InsertLink adds a link, given in its canonical direction, and logs it as
// one event (absent → the other end's ID) on the issue that owns it, whose
// updated_at moves to at. The caller has checked that the link is new and
// allowed.
func InsertLink(ctx context.Context, tx *sql.Tx, l Link, at time.Time, by string) error {
	if err := InsertLinkUnlogged(ctx, tx, l); err != nil {
		return err
	}
	issueID, attr, other := linkEvent(l)
	return logLinkChange(ctx, tx, issueID, attr, nil, other, at, by)
}

// InsertLinkUnlogged adds a link, given in its canonical direction, that an
// issue arrives with: like the links an issue is created with, it logs no
// event and moves no updated_at (ADR-0013). It is for import; a link made on
// an issue that is already here goes through InsertLink.
func InsertLinkUnlogged(ctx context.Context, tx *sql.Tx, l Link) error {
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO links (from_id, type, to_id) VALUES (?, ?, ?)`, l.From, l.Type, l.To); err != nil {
		return fmt.Errorf("link %s %s %s: %w", l.From, l.Type, l.To, err)
	}
	return nil
}

// DeleteLink removes a link, given as it is stored, and logs it as one event
// (the other end's ID → absent) on the issue that owns it.
func DeleteLink(ctx context.Context, tx *sql.Tx, l Link, at time.Time, by string) error {
	res, err := tx.ExecContext(ctx,
		`DELETE FROM links WHERE from_id = ? AND type = ? AND to_id = ?`, l.From, l.Type, l.To)
	if err != nil {
		return fmt.Errorf("unlink %s %s %s: %w", l.From, l.Type, l.To, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("unlink %s %s %s: no such link", l.From, l.Type, l.To)
	}
	issueID, attr, other := linkEvent(l)
	return logLinkChange(ctx, tx, issueID, attr, other, nil, at, by)
}

func logLinkChange(ctx context.Context, tx *sql.Tx, issueID, attr string, old, new any, at time.Time, by string) error {
	if err := insertEvent(ctx, tx, issueID, at, by, attr, old, new); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE issues SET updated_at = ? WHERE id = ?`, FormatTime(at), issueID); err != nil {
		return fmt.Errorf("update %s: %w", issueID, err)
	}
	return nil
}
