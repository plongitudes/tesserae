package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// ListFilter selects issues for a list. The zero value is every live issue
// that isn't done or omit. The conditions combine with AND.
type ListFilter struct {
	Status   string // the displayed status, so waiting can be asked for
	Type     string
	Label    string
	Assignee string
	Parent   string // an exact ID: its descendants at any depth
	Search   string // substring of the id, title, description, or notes; ASCII case is ignored
	Ready    bool   // displays as ready: not started, not blocked, no open children
	Blocked  bool   // in blocked_issues
	All      bool   // done and omit issues too
	Retired  bool   // only retired issues, of any status
	Limit    int    // 0 for no limit
}

// ListIssues returns the issues f selects, in list order: priority, then
// created_at, then id. Ready and blocked are read from the readiness views
// (ADR-0007), and whether an issue is retired from live_issues (ADR-0010).
// Done and omit issues are left out unless f asks for them, by All or by
// naming one as the Status.
func ListIssues(ctx context.Context, tx *sql.Tx, f ListFilter) ([]Issue, error) {
	var where []string
	var args []any
	add := func(cond string, a ...any) {
		where = append(where, cond)
		args = append(args, a...)
	}

	if f.Retired {
		add(`i.id NOT IN (SELECT id FROM live_issues)`)
	} else {
		add(`i.id IN (SELECT id FROM live_issues)`)
		if !f.All && f.Status == "" {
			add(`i.status NOT IN ('done', 'omit')`)
		}
	}
	if f.Status != "" {
		add(`coalesce(s.display_status, i.status) = ?`, f.Status)
	}
	if f.Ready {
		add(`s.display_status = 'ready'`)
	}
	if f.Blocked {
		add(`i.id IN (SELECT id FROM blocked_issues)`)
	}
	if f.Type != "" {
		add(`i.type = ?`, f.Type)
	}
	if f.Label != "" {
		add(`EXISTS (SELECT 1 FROM labels l WHERE l.issue_id = i.id AND l.label = ?)`, f.Label)
	}
	if f.Assignee != "" {
		add(`i.assignee = ?`, f.Assignee)
	}
	if f.Parent != "" {
		add(`i.id IN (SELECT id FROM issue_ancestors WHERE ancestor_id = ?)`, f.Parent)
	}
	if f.Search != "" {
		add(`(instr(lower(i.id), lower(?)) > 0 OR instr(lower(i.title), lower(?)) > 0
		      OR instr(lower(d.body), lower(?)) > 0 OR instr(lower(n.body), lower(?)) > 0)`,
			f.Search, f.Search, f.Search, f.Search)
	}

	tail := `WHERE ` + strings.Join(where, ` AND `) + ` ORDER BY i.priority, i.created_at, i.id`
	if f.Limit > 0 {
		tail += ` LIMIT ?`
		args = append(args, f.Limit)
	}
	issues, err := queryIssues(ctx, tx, tail, args...)
	if err != nil {
		return nil, fmt.Errorf("list issues: %w", err)
	}
	return issues, nil
}
