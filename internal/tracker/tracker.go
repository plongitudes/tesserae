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
	"io"
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
	// History by Get with WithHistory, Tree by Get with WithTree; elsewhere
	// they are nil.
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

	CodeInvalidStatus     = "invalid_status"
	CodeReasonRequired    = "reason_required"
	CodeInvalidTransition = "invalid_transition"
	CodeHasOpenChildren   = "has_open_children"
	CodeBlocked           = "blocked"

	CodeCycle     = "cycle"
	CodeHasParent = "has_parent"
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

// Statuses are the values a status can be set to, not started being ready.
// waiting is how a ready issue displays while it's blocked or has open
// children; it is derived and can't be set (ADR-0009).
var Statuses = []string{"ready", "in-progress", "pending-review", "held", "done", "omit"}

const defaultPriority = 2

// Change is the result of a command that can unblock other issues: the
// issues it changed, in the order they were named, and the issues that were
// blocked before it and aren't after, in list order.
type Change struct {
	Updated        []Issue `json:"updated"`
	NewlyUnblocked []Issue `json:"newly_unblocked"`
}

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
//
// With Append, each text field given is added to the field as a new
// paragraph instead of replacing it: the old text, one blank line, then the
// new text. Append needs at least one text field and can't come with a Title.
type Patch struct {
	Append             bool
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

type getOptions struct{ history, tree bool }

// WithHistory adds the issue's events, oldest first.
func WithHistory() GetOption { return func(o *getOptions) { o.history = true } }

// WithTree adds the dependency tree below the issue: its children and the
// issues it is blocked by, and theirs in turn.
func WithTree() GetOption { return func(o *getOptions) { o.tree = true } }

// Tracker is the interface every command goes through.
type Tracker interface {
	// Create adds an issue, allocating its ID in the same transaction.
	Create(ctx context.Context, n NewIssue) (Issue, error)
	// Get resolves a partial ID and returns the issue with its links,
	// comments, and, for a held issue, its previous status; and with
	// WithHistory and WithTree, its history and the tree below it.
	Get(ctx context.Context, partialID string, opts ...GetOption) (Issue, error)
	// Update applies a patch, logging an event for each field that changed.
	Update(ctx context.Context, partialID string, p Patch) (Issue, error)
	// Status sets the status of each issue named, all of them or none, and
	// changes nothing else but the status reason. force lets a blocked
	// issue be marked done.
	Status(ctx context.Context, partialIDs []string, status, reason string, force bool) (Change, error)
	// Link adds a link `a WORDING b`, in any of LinkWordings, for every
	// pair of an issue on the left and one on the right, in that order (all
	// of the first left issue's pairs, then the next's), and returns them in
	// their canonical direction. All are added or none. A link that is
	// already there, in either wording, is returned as it is and nothing
	// changes for it.
	Link(ctx context.Context, left []string, wording string, right []string) ([]Link, error)
	// Unlink removes the link `a WORDING b` for every pair, all of them or
	// none, and returns the links removed.
	Unlink(ctx context.Context, left []string, wording string, right []string) ([]Link, error)
	// Retire hides each issue named, all of them or none, and reports the
	// issues that are no longer blocked because of it. A reason, if given,
	// is kept as a comment on each.
	Retire(ctx context.Context, partialIDs []string, reason string) (Change, error)
	// Restore brings a retired issue back. It takes the exact ID only.
	Restore(ctx context.Context, id string) (Issue, error)
	// Comment adds an immutable comment by the actor.
	Comment(ctx context.Context, partialID, text string) (Comment, error)
	// List returns the issues a filter selects, in list order: priority,
	// then created_at, then id.
	List(ctx context.Context, f Filter) ([]Issue, error)
	// ActiveBlockers returns the active blockers of every issue that has
	// any, by issue ID, each in list order. A blocked issue that isn't in
	// it is blocked through an ancestor.
	ActiveBlockers(ctx context.Context) (map[string][]string, error)
	// Import adds the issues of an old bd issues.jsonl, keeping their IDs
	// and skipping any that are already here, all of them or none.
	Import(ctx context.Context, r io.Reader) (ImportResult, error)
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
	prefix, ok, err := t.store.Config(ctx, configPrefix)
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
			// The links a new issue arrives with are held to the rule for
			// a blocks link inside a hierarchy too (ADR-0016). Nothing else
			// can go wrong: a new issue closes no loop.
			if row.Parent != "" {
				above, err := isOrIsUnder(ctx, tx, row.Parent, blocker)
				if err != nil {
					return err
				}
				if above {
					return hierarchyError(blocker, fmt.Sprintf(
						"%s is an ancestor of the new issue, a child of %s, so the new issue could never start; %s blocking it would make a dependency loop",
						blocker, row.Parent, blocker))
				}
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
			if err := sqlite.LoadHistory(ctx, tx, &issue); err != nil {
				return err
			}
		}
		if o.tree {
			return sqlite.LoadTree(ctx, tx, &issue)
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
	if p.Append {
		switch {
		case p.Title != nil:
			checks = append(checks, invalid("--append adds to a text, and a title is not one",
				"it applies to the description, design, acceptance criteria, and notes; change the title without it"))
		case p.Description == nil && p.Design == nil && p.AcceptanceCriteria == nil && p.Notes == nil:
			checks = append(checks, invalid("--append was given no text to add",
				"give the text with -d, --design, --acceptance, or --notes"))
		}
	}
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
			Append: p.Append,
			Title:  p.Title, Type: p.Type, Priority: p.Priority,
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

func (t *tracker) Status(ctx context.Context, partialIDs []string, status, reason string, force bool) (Change, error) {
	if !slices.Contains(Statuses, status) {
		e := &Error{
			Code:    CodeInvalidStatus,
			Message: fmt.Sprintf("%q is not a status that can be set", status),
			Hint:    "statuses: " + strings.Join(Statuses, ", "),
		}
		if status == "waiting" {
			e.Hint = "waiting is how a ready issue shows while it's blocked or has open children; set ready"
		}
		return Change{}, e
	}
	if len(partialIDs) == 0 {
		return Change{}, invalid("name at least one issue", "")
	}
	if (status == "held" || status == "omit") && strings.TrimSpace(reason) == "" {
		return Change{}, &Error{
			Code:    CodeReasonRequired,
			Message: fmt.Sprintf("setting %s needs a reason", status),
			Hint:    "give one with -r",
		}
	}

	var change Change
	err := t.store.Write(ctx, func(tx *sql.Tx) error {
		// Each change is judged against the changes before it in the same
		// command, so a blocker and its dependent can be finished together.
		blockedBefore, err := sqlite.BlockedIssues(ctx, tx)
		if err != nil {
			return err
		}
		var changed []string
		for _, partial := range partialIDs {
			id, err := resolveLive(ctx, tx, partial)
			if err != nil {
				return err
			}
			if slices.Contains(changed, id) {
				continue
			}
			if err := t.setStatus(ctx, tx, id, status, reason, force); err != nil {
				return err
			}
			changed = append(changed, id)
		}
		unblocked, err := sqlite.NewlyUnblocked(ctx, tx, blockedBefore)
		if err != nil {
			return err
		}
		if change.Updated, err = readAll(ctx, tx, changed); err != nil {
			return err
		}
		change.NewlyUnblocked, err = readAll(ctx, tx, unblocked)
		return err
	})
	if err != nil {
		return Change{}, err
	}
	return change, nil
}

// setStatus is one issue's status change: the transition rules of ADR-0018,
// then the write. Setting the status an issue already has is not a
// transition: no rule applies, and only a reason that was given is written.
func (t *tracker) setStatus(ctx context.Context, tx *sql.Tx, id, status, reason string, force bool) error {
	current, err := sqlite.StoredStatus(ctx, tx, id)
	if err != nil {
		return err
	}
	patch := sqlite.Patch{Status: &status}
	if current != status {
		if err := checkTransition(ctx, tx, id, current, status, force); err != nil {
			return err
		}
		// The reason belongs to the status: a new status without one clears
		// the old status's reason.
		patch.StatusReason = &reason
	} else if reason != "" {
		patch.StatusReason = &reason
	}
	return sqlite.UpdateIssue(ctx, tx, id, patch, t.now(), t.actor)
}

func checkTransition(ctx context.Context, tx *sql.Tx, id, from, to string, force bool) error {
	terminal := func(s string) bool { return s == "done" || s == "omit" }
	if to == "held" && terminal(from) {
		return &Error{
			Code:    CodeInvalidTransition,
			Message: fmt.Sprintf("%s is %s and can't be held", id, from),
			Hint:    fmt.Sprintf("tess status %s ready reopens it", id),
		}
	}
	if !terminal(to) {
		return nil
	}
	open, err := sqlite.OpenChildren(ctx, tx, id)
	if err != nil {
		return err
	}
	if len(open) > 0 {
		return &Error{
			Code:    CodeHasOpenChildren,
			Message: fmt.Sprintf("%s has open children: %s", id, strings.Join(open, ", ")),
			Hint:    "set each to done or omit first",
		}
	}
	if to == "done" && !force {
		blocked, err := sqlite.IsBlocked(ctx, tx, id)
		if err != nil {
			return err
		}
		if blocked {
			return &Error{
				Code:    CodeBlocked,
				Message: fmt.Sprintf("%s is blocked", id),
				Hint:    fmt.Sprintf("tess show %s lists what blocks it; --force marks it done anyway", id),
			}
		}
	}
	return nil
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
		return "", retiredError(id)
	}
	return id, nil
}

func retiredError(id string) *Error {
	return &Error{
		Code:    CodeRetired,
		Message: fmt.Sprintf("%s is retired", id),
		Hint:    fmt.Sprintf("tess restore %s brings it back", id),
	}
}

func read(ctx context.Context, tx *sql.Tx, id string) (Issue, error) {
	issue, ok, err := sqlite.GetIssue(ctx, tx, id)
	if err == nil && !ok {
		err = fmt.Errorf("issue %s is missing", id)
	}
	return issue, err
}

// readAll reads issues in the order given; the result is never nil.
func readAll(ctx context.Context, tx *sql.Tx, ids []string) ([]Issue, error) {
	issues := make([]Issue, 0, len(ids))
	for _, id := range ids {
		issue, err := read(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		issues = append(issues, issue)
	}
	return issues, nil
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
