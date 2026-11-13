// Package tracker is the issue tracker's behaviour: everything a command
// does goes through the Tracker interface, and cli is only an adapter over
// it (docs/design/module-map.md). The clock and the actor are passed in;
// nothing here reads the environment or the wall clock.
package tracker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/plongitudes/tesserae/internal/ids"
	"github.com/plongitudes/tesserae/internal/store/sqlite"
)

// The types an issue reads as. They are the store's, which is where the
// reads are written; callers use these names.
type (
	// Issue is the issue object of acceptance/README.md. Status is the
	// displayed status. Links, Comments and PreviousStatus are set by Get,
	// History by Get with WithHistory; elsewhere they are nil.
	Issue   = sqlite.Issue
	Links   = sqlite.Links
	Comment = sqlite.Comment
	Event   = sqlite.Event
)

// Error codes, as they appear in the CLI's error envelope.
const (
	CodeNotFound        = ids.CodeNotFound
	CodeAmbiguousID     = ids.CodeAmbiguous
	CodeInvalidArgument = "invalid_argument"
	CodeRetired         = "retired"
)

// Error is a refusal with a stable code. Hint, when set, is a one-line
// suggestion.
type Error struct {
	Code    string
	Message string
	Hint    string
}

func (e *Error) Error() string { return e.Message }

// IssueTypes are the values an issue's type can take.
var IssueTypes = []string{"task", "bug", "feature", "epic", "chore"}

const defaultPriority = 2

// NewIssue is what a caller gives to create an issue. Only Title is needed.
type NewIssue struct {
	Title              string
	Description        string
	Design             string
	AcceptanceCriteria string
	Notes              string
	Type               string // "" for task
	Priority           *int   // nil for 2
	Assignee           string
	Labels             []string
	Parent             string   // an ID, partial or whole; "" for none
	BlockedBy          []string // IDs, partial or whole
}

// Patch is a typed change to any field but status: nil fields are left
// alone. For Assignee and Owner an empty string clears the value, and a zero
// DueAt clears the due date. The text fields are taken verbatim.
type Patch struct {
	Title              *string
	Type               *string
	Priority           *int
	Description        *string
	Design             *string
	AcceptanceCriteria *string
	Notes              *string
	Assignee           *string
	Owner              *string
	DueAt              *time.Time
	AddLabels          []string
	RemoveLabels       []string
}

// GetOption asks Get for more than the issue and its detail.
type GetOption func(*getOptions)

type getOptions struct{ history bool }

// WithHistory adds the issue's events, oldest first.
func WithHistory() GetOption { return func(o *getOptions) { o.history = true } }

// Tracker is the interface every command goes through.
type Tracker interface {
	// Create adds an issue, allocating its ID in the same transaction.
	Create(ctx context.Context, n NewIssue) (Issue, error)
	// Get resolves a partial ID and returns the issue with its links,
	// comments, and, for a held issue, its previous status.
	Get(ctx context.Context, partialID string, opts ...GetOption) (Issue, error)
	// Update applies a patch, logging an event for each field that changed.
	Update(ctx context.Context, partialID string, p Patch) (Issue, error)
}

type tracker struct {
	store *sqlite.Store
	now   func() time.Time
	actor string
}

// New returns a Tracker over store. now is the clock for every timestamp it
// writes, and actor is who its changes are recorded as.
func New(store *sqlite.Store, now func() time.Time, actor string) Tracker {
	return &tracker{store: store, now: now, actor: actor}
}

func (t *tracker) Create(ctx context.Context, n NewIssue) (Issue, error) {
	if strings.TrimSpace(n.Title) == "" {
		return Issue{}, invalid("an issue needs a title", "")
	}
	if n.Type == "" {
		n.Type = IssueTypes[0]
	}
	priority := defaultPriority
	if n.Priority != nil {
		priority = *n.Priority
	}
	if err := errors.Join(checkType(n.Type), checkPriority(priority), checkLabels(n.Labels)); err != nil {
		return Issue{}, firstError(err)
	}
	prefix, ok, err := t.store.Config(ctx, "prefix")
	if err != nil {
		return Issue{}, err
	}
	if !ok {
		return Issue{}, errors.New("the database has no ID prefix configured")
	}

	now := t.now()
	var issue Issue
	err = t.store.Write(ctx, func(tx *sql.Tx) error {
		row := sqlite.NewIssue{
			Title: n.Title, Description: n.Description, Design: n.Design,
			AcceptanceCriteria: n.AcceptanceCriteria, Notes: n.Notes,
			Type: n.Type, Priority: priority, Assignee: n.Assignee, Labels: n.Labels,
			CreatedAt: now, CreatedBy: t.actor,
		}
		if n.Parent != "" {
			if row.Parent, err = resolveLive(ctx, tx, n.Parent); err != nil {
				return err
			}
		}
		for _, partial := range n.BlockedBy {
			blocker, err := resolveLive(ctx, tx, partial)
			if err != nil {
				return err
			}
			row.BlockedBy = append(row.BlockedBy, blocker)
		}

		count, err := sqlite.IssueCount(ctx, tx)
		if err != nil {
			return err
		}
		seed := ids.Seed{Prefix: prefix, Title: n.Title, Creator: t.actor, CreatedAt: now}
		row.ID, err = ids.Allocate(seed, count, func(id string) (bool, error) {
			return sqlite.IssueExists(ctx, tx, id)
		})
		if err != nil {
			return err
		}
		if err := sqlite.InsertIssue(ctx, tx, row); err != nil {
			return err
		}
		issue, err = read(ctx, tx, row.ID)
		return err
	})
	return issue, err
}

func (t *tracker) Get(ctx context.Context, partialID string, opts ...GetOption) (Issue, error) {
	var o getOptions
	for _, opt := range opts {
		opt(&o)
	}
	var issue Issue
	err := t.store.Read(ctx, func(tx *sql.Tx) error {
		id, _, err := resolve(ctx, tx, partialID)
		if err != nil {
			return err
		}
		if issue, err = read(ctx, tx, id); err != nil {
			return err
		}
		if err := sqlite.LoadDetail(ctx, tx, &issue); err != nil {
			return err
		}
		if o.history {
			return sqlite.LoadHistory(ctx, tx, &issue)
		}
		return nil
	})
	return issue, err
}

func (t *tracker) Update(ctx context.Context, partialID string, p Patch) (Issue, error) {
	var checks []error
	if p.Title != nil && strings.TrimSpace(*p.Title) == "" {
		checks = append(checks, invalid("an issue needs a title", ""))
	}
	if p.Type != nil {
		checks = append(checks, checkType(*p.Type))
	}
	if p.Priority != nil {
		checks = append(checks, checkPriority(*p.Priority))
	}
	checks = append(checks, checkLabels(p.AddLabels), checkLabels(p.RemoveLabels))
	if err := errors.Join(checks...); err != nil {
		return Issue{}, firstError(err)
	}

	var issue Issue
	err := t.store.Write(ctx, func(tx *sql.Tx) error {
		id, err := resolveLive(ctx, tx, partialID)
		if err != nil {
			return err
		}
		change := sqlite.Patch{
			Title: p.Title, Type: p.Type, Priority: p.Priority,
			Description: p.Description, Design: p.Design,
			AcceptanceCriteria: p.AcceptanceCriteria, Notes: p.Notes,
			Assignee: p.Assignee, Owner: p.Owner, DueAt: p.DueAt,
			AddLabels: p.AddLabels, RemoveLabels: p.RemoveLabels,
		}
		if err := sqlite.UpdateIssue(ctx, tx, id, change, t.now(), t.actor); err != nil {
			return err
		}
		issue, err = read(ctx, tx, id)
		return err
	})
	return issue, err
}

// resolve turns a typed ID into the ID of the issue it means, and says
// whether that issue is retired (only an exact ID reaches a retired issue).
func resolve(ctx context.Context, tx *sql.Tx, partialID string) (id string, retired bool, err error) {
	found, err := sqlite.IDCandidates(ctx, tx, partialID)
	if err != nil {
		return "", false, err
	}
	candidates := make([]ids.Candidate, len(found))
	for i, c := range found {
		candidates[i] = ids.Candidate(c)
	}
	id, err = ids.Resolve(partialID, candidates)
	var notFound *ids.NotFoundError
	var ambiguous *ids.AmbiguousError
	switch {
	case errors.As(err, &notFound):
		return "", false, &Error{Code: CodeNotFound, Message: err.Error(), Hint: "tess list shows the issues there are"}
	case errors.As(err, &ambiguous):
		return "", false, &Error{Code: CodeAmbiguousID, Message: err.Error(), Hint: "type more of the ID"}
	case err != nil:
		return "", false, err
	}
	i := slices.IndexFunc(candidates, func(c ids.Candidate) bool { return c.ID == id })
	return id, candidates[i].Retired, nil
}

// resolveLive is resolve for an issue about to be changed or linked to: a
// retired issue is refused.
func resolveLive(ctx context.Context, tx *sql.Tx, partialID string) (string, error) {
	id, retired, err := resolve(ctx, tx, partialID)
	if err != nil {
		return "", err
	}
	if retired {
		return "", &Error{
			Code:    CodeRetired,
			Message: fmt.Sprintf("%s is retired", id),
			Hint:    fmt.Sprintf("tess restore %s brings it back", id),
		}
	}
	return id, nil
}

func read(ctx context.Context, tx *sql.Tx, id string) (Issue, error) {
	issue, ok, err := sqlite.GetIssue(ctx, tx, id)
	if err == nil && !ok {
		err = fmt.Errorf("issue %s is missing", id)
	}
	return issue, err
}

func invalid(message, hint string) *Error {
	return &Error{Code: CodeInvalidArgument, Message: message, Hint: hint}
}

func checkType(typ string) error {
	if slices.Contains(IssueTypes, typ) {
		return nil
	}
	return invalid(fmt.Sprintf("unknown type %q", typ), "types: "+strings.Join(IssueTypes, ", "))
}

func checkPriority(p int) error {
	if p >= 0 && p <= 4 {
		return nil
	}
	return invalid(fmt.Sprintf("priority %d is out of range", p), "priorities run from 0 (highest) to 4")
}

func checkLabels(labels []string) error {
	for _, l := range labels {
		if strings.TrimSpace(l) == "" {
			return invalid("a label can't be empty", "")
		}
	}
	return nil
}

// firstError picks the first *Error out of joined check results.
func firstError(err error) error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return err
}
