package tracker

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"github.com/plongitudes/tesserae/internal/store/sqlite"
)

// DisplayedStatuses are the statuses an issue can show, which is what a list
// filters on: the settable ones and the derived waiting.
var DisplayedStatuses = []string{"ready", "waiting", "in-progress", "pending-review", "held", "done", "omit"}

// Filter selects the issues List returns. The zero value is every issue
// that isn't done, omit, or retired; the conditions set combine with AND.
type Filter struct {
	Status   string // a displayed status; naming done or omit includes it
	Type     string
	Label    string
	Assignee string
	Parent   string // an ID, partial or whole: its descendants at any depth
	Search   string // case-insensitive substring of id, title, description, or notes
	Ready    bool   // only issues that display as ready (`tess ready`)
	Blocked  bool   // only blocked issues
	All      bool   // include done and omit issues
	Retired  bool   // only retired issues, which no other list shows
	Limit    int    // at most this many; 0 for all
}

func (t *tracker) List(ctx context.Context, f Filter) ([]Issue, error) {
	if f.Status != "" && !slices.Contains(DisplayedStatuses, f.Status) {
		return nil, &Error{
			Code:    CodeInvalidStatus,
			Message: fmt.Sprintf("%q is not a status", f.Status),
			Hint:    "statuses: " + strings.Join(DisplayedStatuses, ", "),
		}
	}
	if f.Type != "" {
		if err := checkType(f.Type); err != nil {
			return nil, err
		}
	}
	if f.Limit < 0 {
		return nil, invalid(fmt.Sprintf("a limit of %d makes no sense", f.Limit), "give a positive number, or leave it out for no limit")
	}

	var issues []Issue
	err := t.store.Read(ctx, func(tx *sql.Tx) error {
		q := sqlite.ListFilter{
			Status: f.Status, Type: f.Type, Label: f.Label, Assignee: f.Assignee, Search: f.Search,
			Ready: f.Ready, Blocked: f.Blocked, All: f.All, Retired: f.Retired, Limit: f.Limit,
		}
		if f.Parent != "" {
			// A retired parent is reached by its exact ID and has no
			// descendants while it's retired.
			parent, _, err := resolve(ctx, tx, f.Parent)
			if err != nil {
				return err
			}
			q.Parent = parent
		}
		var err error
		issues, err = sqlite.ListIssues(ctx, tx, q)
		return err
	})
	return issues, err
}

func (t *tracker) ActiveBlockers(ctx context.Context) (map[string][]string, error) {
	var blockers map[string][]string
	err := t.store.Read(ctx, func(tx *sql.Tx) error {
		var err error
		blockers, err = sqlite.ActiveBlockers(ctx, tx)
		return err
	})
	return blockers, err
}
