package tracker_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/plongitudes/tesserae/internal/store/sqlite"
	"github.com/plongitudes/tesserae/internal/tracker"
)

// The hash every text field holds before it's first written.
const emptyTextHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

var start = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// fixture is a tracker over a :memory: store, with a clock the test moves.
type fixture struct {
	t   *testing.T
	ctx context.Context
	s   *sqlite.Store
	tr  tracker.Tracker
	now time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	s, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	f := &fixture{t: t, ctx: ctx, s: s, now: start}
	f.write(func(tx *sql.Tx) error { return sqlite.SetConfig(ctx, tx, "prefix", "t") })
	f.tr = tracker.New(s, func() time.Time { return f.now }, "tester")
	return f
}

// write sets up state that this item's interface can't reach yet (retiring,
// status changes, links between existing issues, comments).
func (f *fixture) write(fn func(tx *sql.Tx) error) {
	f.t.Helper()
	if err := f.s.Write(f.ctx, fn); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) exec(query string, args ...any) {
	f.t.Helper()
	f.write(func(tx *sql.Tx) error {
		_, err := tx.ExecContext(f.ctx, query, args...)
		return err
	})
}

func (f *fixture) retire(id string) {
	f.t.Helper()
	f.exec(`UPDATE issues SET retired_at = '2026-01-05T00:00:00.000Z' WHERE id = ?`, id)
}

func (f *fixture) setStatus(id, status string) {
	f.t.Helper()
	f.write(func(tx *sql.Tx) error {
		return sqlite.UpdateIssue(f.ctx, tx, id, sqlite.Patch{Status: &status}, f.now, "tester")
	})
}

func (f *fixture) tick() { f.now = f.now.Add(time.Second) }

func (f *fixture) create(n tracker.NewIssue) tracker.Issue {
	f.t.Helper()
	issue, err := f.tr.Create(f.ctx, n)
	if err != nil {
		f.t.Fatalf("Create(%+v): %v", n, err)
	}
	return issue
}

func (f *fixture) titled(title string) string {
	f.t.Helper()
	return f.create(tracker.NewIssue{Title: title}).ID
}

func (f *fixture) get(id string, opts ...tracker.GetOption) tracker.Issue {
	f.t.Helper()
	issue, err := f.tr.Get(f.ctx, id, opts...)
	if err != nil {
		f.t.Fatalf("Get(%q): %v", id, err)
	}
	return issue
}

func (f *fixture) update(id string, p tracker.Patch) tracker.Issue {
	f.t.Helper()
	issue, err := f.tr.Update(f.ctx, id, p)
	if err != nil {
		f.t.Fatalf("Update(%q, %+v): %v", id, p, err)
	}
	return issue
}

// change is the part of an event a test states: what changed, from, to.
type change struct {
	Attr     string
	Old, New any
}

func (f *fixture) changes(id string) []change {
	f.t.Helper()
	out := []change{}
	for _, e := range f.get(id, tracker.WithHistory()).History {
		out = append(out, change{e.Attr, e.OldValue, e.NewValue})
	}
	return out
}

func (f *fixture) wantChanges(id string, want ...change) {
	f.t.Helper()
	if want == nil {
		want = []change{}
	}
	if got := f.changes(id); !reflect.DeepEqual(got, want) {
		f.t.Errorf("events of %s:\n got  %#v\n want %#v", id, got, want)
	}
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var te *tracker.Error
	if !errors.As(err, &te) || te.Code != code {
		t.Errorf("error = %v; want code %s", err, code)
	}
}

func wantStrings(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v; want %#v", what, got, want)
	}
}

func ptr[T any](v T) *T { return &v }

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestCreateFillsInDefaults(t *testing.T) {
	f := newFixture(t)
	issue := f.create(tracker.NewIssue{Title: "Plain issue"})

	if !regexp.MustCompile(`^t-[0-9a-z]{3,}$`).MatchString(issue.ID) {
		t.Errorf("ID = %q; want t-<base36>", issue.ID)
	}
	want := tracker.Issue{
		ID: issue.ID, Title: "Plain issue", Type: "task", Priority: 2, Status: "ready",
		Labels: []string{}, Ancestors: []string{},
		CreatedAt: start, CreatedBy: "tester", UpdatedAt: start,
	}
	if !reflect.DeepEqual(issue, want) {
		t.Errorf("created issue:\n got  %+v\n want %+v", issue, want)
	}

	// Get adds the detail, with empty collections present.
	want.Links = &tracker.Links{
		Blocks: []string{}, BlockedBy: []string{}, Children: []string{},
		Related: []string{}, DiscoveredFrom: []string{}, Discovered: []string{},
	}
	want.Comments = []tracker.Comment{}
	if got := f.get(issue.ID); !reflect.DeepEqual(got, want) {
		t.Errorf("Get:\n got  %+v\n want %+v", got, want)
	}
}

func TestCreateStoresEveryField(t *testing.T) {
	f := newFixture(t)
	issue := f.create(tracker.NewIssue{
		Title: "Full issue", Type: "bug", Priority: ptr(0),
		Description: "line one\nline two\n", Design: "Use a view",
		AcceptanceCriteria: "ready stays fast", Notes: "Seen twice",
		Labels: []string{"ui", "auth", "ui"}, Assignee: "alice",
	})
	got := f.get(issue.ID)

	if got.Type != "bug" || got.Priority != 0 {
		t.Errorf("type, priority = %s, %d; want bug, 0", got.Type, got.Priority)
	}
	if got.Description != "line one\nline two\n" || got.Design != "Use a view" ||
		got.AcceptanceCriteria != "ready stays fast" || got.Notes != "Seen twice" {
		t.Errorf("texts = %q, %q, %q, %q", got.Description, got.Design, got.AcceptanceCriteria, got.Notes)
	}
	wantStrings(t, "labels", got.Labels, "auth", "ui")
	if got.Assignee == nil || *got.Assignee != "alice" {
		t.Errorf("assignee = %v; want alice", got.Assignee)
	}
}

func TestCreateRefusesBadValues(t *testing.T) {
	f := newFixture(t)
	for name, n := range map[string]tracker.NewIssue{
		"no title":       {},
		"blank title":    {Title: "  "},
		"unknown type":   {Title: "x", Type: "saga"},
		"priority 7":     {Title: "x", Priority: ptr(7)},
		"priority -1":    {Title: "x", Priority: ptr(-1)},
		"empty label":    {Title: "x", Labels: []string{""}},
		"status as type": {Title: "x", Type: "ready"},
	} {
		_, err := f.tr.Create(f.ctx, n)
		if err == nil {
			t.Errorf("%s: created", name)
			continue
		}
		wantCode(t, err, tracker.CodeInvalidArgument)
	}

	_, err := f.tr.Create(f.ctx, tracker.NewIssue{Title: "x", Parent: "t-nope"})
	wantCode(t, err, tracker.CodeNotFound)
	_, err = f.tr.Create(f.ctx, tracker.NewIssue{Title: "x", BlockedBy: []string{"t-nope"}})
	wantCode(t, err, tracker.CodeNotFound)

	// Nothing was left behind by the refused creates.
	_, err = f.tr.Get(f.ctx, "t-")
	wantCode(t, err, tracker.CodeNotFound)
}

func TestCreatingTheSameIssueTwiceGivesTwoIDs(t *testing.T) {
	f := newFixture(t)
	// Same title, creator and instant: the second create collides and is
	// stepped around inside its transaction.
	a, b := f.titled("Same"), f.titled("Same")
	if a == b {
		t.Fatalf("both creates returned %s", a)
	}
	if f.get(a).ID != a || f.get(b).ID != b {
		t.Errorf("the two issues aren't both there")
	}
}

func TestGetResolvesPartialIDs(t *testing.T) {
	f := newFixture(t)
	a := f.titled("A")
	f.titled("B")

	if got := f.get(a[2:]); got.ID != a { // the bare hash
		t.Errorf("Get(%q) = %s; want %s", a[2:], got.ID, a)
	}
	_, err := f.tr.Get(f.ctx, "t-")
	wantCode(t, err, tracker.CodeAmbiguousID)
	_, err = f.tr.Get(f.ctx, "t-zzzzzzzzzz")
	wantCode(t, err, tracker.CodeNotFound)
}

func TestAnIssueCreatedBlockedByAnotherIsWaiting(t *testing.T) {
	f := newFixture(t)
	blocker := f.titled("Blocker")
	// The blocker is named by a partial ID.
	dep := f.create(tracker.NewIssue{Title: "Dependent", BlockedBy: []string{blocker[2:], blocker}})

	if dep.Status != "waiting" || !dep.Blocked {
		t.Errorf("dependent: status %s, blocked %v; want waiting, true", dep.Status, dep.Blocked)
	}
	wantStrings(t, "dependent blocked_by", f.get(dep.ID).Links.BlockedBy, blocker)

	b := f.get(blocker)
	if b.Status != "ready" || b.Blocked {
		t.Errorf("blocker: status %s, blocked %v; want ready, false", b.Status, b.Blocked)
	}
	wantStrings(t, "blocker blocks", b.Links.Blocks, dep.ID)
}

func TestAnIssueKeepsItsStatusWhileBlocked(t *testing.T) {
	f := newFixture(t)
	blocker := f.titled("Blocker")
	dep := f.create(tracker.NewIssue{Title: "Dependent", BlockedBy: []string{blocker}}).ID

	f.setStatus(dep, "in-progress")
	if got := f.get(dep); got.Status != "in-progress" || !got.Blocked {
		t.Errorf("status %s, blocked %v; want in-progress, true", got.Status, got.Blocked)
	}

	// A done blocker is no longer active.
	f.setStatus(dep, "ready")
	f.setStatus(blocker, "done")
	if got := f.get(dep); got.Status != "ready" || got.Blocked {
		t.Errorf("after the blocker is done: status %s, blocked %v; want ready, false", got.Status, got.Blocked)
	}
}

func TestChildrenAndAncestors(t *testing.T) {
	f := newFixture(t)
	top := f.titled("Top")
	mid := f.create(tracker.NewIssue{Title: "Middle", Parent: top}).ID
	leaf := f.create(tracker.NewIssue{Title: "Leaf", Parent: mid})
	other := f.create(tracker.NewIssue{Title: "Other", Parent: top}).ID

	if leaf.Parent == nil || *leaf.Parent != mid {
		t.Errorf("leaf parent = %v; want %s", leaf.Parent, mid)
	}
	wantStrings(t, "leaf ancestors", leaf.Ancestors, mid, top) // nearest first
	if leaf.Status != "ready" || leaf.Blocked {
		t.Errorf("leaf: status %s, blocked %v; want ready, false", leaf.Status, leaf.Blocked)
	}

	// Open children make a parent waiting, not blocked.
	for _, id := range []string{top, mid} {
		if got := f.get(id); got.Status != "waiting" || got.Blocked {
			t.Errorf("%s: status %s, blocked %v; want waiting, false", got.Title, got.Status, got.Blocked)
		}
	}

	children := []string{mid, other}
	if mid > other {
		children = []string{other, mid}
	}
	wantStrings(t, "top children", f.get(top).Links.Children, children...)

	// A held ancestor blocks its descendants, which keep their stored status.
	f.setStatus(top, "held")
	if got := f.get(leaf.ID); got.Status != "waiting" || !got.Blocked {
		t.Errorf("leaf under a held ancestor: status %s, blocked %v; want waiting, true", got.Status, got.Blocked)
	}
}

func TestGetShowsRelatedAndDiscoveredLinksFromBothEnds(t *testing.T) {
	f := newFixture(t)
	a, b := f.titled("A"), f.titled("B")
	f.exec(`INSERT INTO links (from_id, type, to_id) VALUES (?, 'related', ?)`, a, b)
	f.exec(`INSERT INTO links (from_id, type, to_id) VALUES (?, 'discovered-from', ?)`, b, a)

	ga, gb := f.get(a), f.get(b)
	wantStrings(t, "A related", ga.Links.Related, b)
	wantStrings(t, "B related", gb.Links.Related, a)
	wantStrings(t, "B discovered_from", gb.Links.DiscoveredFrom, a)
	wantStrings(t, "A discovered", ga.Links.Discovered, b)
	wantStrings(t, "A discovered_from", ga.Links.DiscoveredFrom)
	wantStrings(t, "B discovered", gb.Links.Discovered)
}

func TestRetiredIssues(t *testing.T) {
	f := newFixture(t)
	blocker := f.titled("Mistake")
	dep := f.create(tracker.NewIssue{Title: "Dependent", BlockedBy: []string{blocker}}).ID
	f.retire(blocker)

	// Links to a retired issue are left out, and it no longer blocks.
	got := f.get(dep)
	wantStrings(t, "blocked_by", got.Links.BlockedBy)
	if got.Status != "ready" || got.Blocked {
		t.Errorf("dependent: status %s, blocked %v; want ready, false", got.Status, got.Blocked)
	}

	// The exact ID still reaches it; a partial one doesn't.
	r := f.get(blocker)
	if r.RetiredAt == nil || !r.RetiredAt.Equal(time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("retired_at = %v", r.RetiredAt)
	}
	_, err := f.tr.Get(f.ctx, blocker[2:])
	wantCode(t, err, tracker.CodeNotFound)

	// It can't be changed, or linked to.
	_, err = f.tr.Update(f.ctx, blocker, tracker.Patch{Title: ptr("x")})
	wantCode(t, err, tracker.CodeRetired)
	_, err = f.tr.Create(f.ctx, tracker.NewIssue{Title: "x", Parent: blocker})
	wantCode(t, err, tracker.CodeRetired)
	_, err = f.tr.Create(f.ctx, tracker.NewIssue{Title: "x", BlockedBy: []string{blocker}})
	wantCode(t, err, tracker.CodeRetired)
	f.wantChanges(blocker)
	if f.get(blocker).Title != "Mistake" {
		t.Errorf("a retired issue was changed")
	}
}

func TestGetListsComments(t *testing.T) {
	f := newFixture(t)
	a := f.titled("A")
	f.exec(`INSERT INTO comments (id, issue_id, author, text, created_at) VALUES
	        ('c2', ?1, 'bob', 'Second', '2026-01-03T00:00:00.000Z'),
	        ('c1', ?1, 'alice', 'First', '2026-01-02T00:00:00.000Z')`, a)

	want := []tracker.Comment{
		{ID: "c1", Author: "alice", Text: "First", CreatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
		{ID: "c2", Author: "bob", Text: "Second", CreatedAt: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)},
	}
	if got := f.get(a).Comments; !reflect.DeepEqual(got, want) {
		t.Errorf("comments = %+v; want %+v", got, want)
	}
}

func TestAHeldIssueShowsItsPreviousStatus(t *testing.T) {
	f := newFixture(t)
	a := f.titled("A")
	if got := f.get(a); got.PreviousStatus != nil {
		t.Errorf("previous_status of a ready issue = %q", *got.PreviousStatus)
	}

	// Same instant throughout: the order of events must not depend on the clock.
	f.setStatus(a, "in-progress")
	f.setStatus(a, "pending-review")
	f.setStatus(a, "held")
	got := f.get(a)
	if got.Status != "held" || got.PreviousStatus == nil || *got.PreviousStatus != "pending-review" {
		t.Errorf("status %s, previous_status %v; want held, pending-review", got.Status, got.PreviousStatus)
	}

	f.setStatus(a, "in-progress")
	if got := f.get(a); got.PreviousStatus != nil {
		t.Errorf("previous_status after the hold ended = %q", *got.PreviousStatus)
	}
}

func TestCreationLogsNothing(t *testing.T) {
	f := newFixture(t)
	p := f.titled("Parent")
	b := f.titled("Blocker")
	c := f.create(tracker.NewIssue{
		Title: "Child", Description: "text", Labels: []string{"ui"}, Assignee: "alice",
		Parent: p, BlockedBy: []string{b},
	}).ID

	for _, id := range []string{p, b, c} {
		f.wantChanges(id)
	}
	// History is only there on request.
	if got := f.get(c); got.History != nil {
		t.Errorf("Get without WithHistory returned history %v", got.History)
	}
}

func TestUpdateLogsOneEventPerChangedField(t *testing.T) {
	f := newFixture(t)
	a := f.titled("Original")
	f.now = time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	due := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	got := f.update(a, tracker.Patch{
		Title: ptr("New title"), Type: ptr("feature"), Priority: ptr(1),
		Assignee: ptr("bob"), Owner: ptr("carol"), DueAt: &due,
	})

	if got.Title != "New title" || got.Type != "feature" || got.Priority != 1 ||
		got.Assignee == nil || *got.Assignee != "bob" || got.Owner == nil || *got.Owner != "carol" ||
		got.DueAt == nil || !got.DueAt.Equal(due) {
		t.Errorf("updated issue = %+v", got)
	}
	if !got.UpdatedAt.Equal(f.now) || !got.CreatedAt.Equal(start) {
		t.Errorf("updated_at, created_at = %v, %v; want %v, %v", got.UpdatedAt, got.CreatedAt, f.now, start)
	}
	if got.Status != "ready" {
		t.Errorf("status = %s; an update must not touch it", got.Status)
	}

	f.wantChanges(a,
		change{"title", "Original", "New title"},
		change{"type", "task", "feature"},
		change{"priority", 2, 1}, // numbers stay numbers
		change{"assignee", nil, "bob"},
		change{"owner", nil, "carol"},
		change{"due_at", nil, "2026-02-01T00:00:00Z"},
	)
	for _, e := range f.get(a, tracker.WithHistory()).History {
		if e.ID == "" || e.ChangedBy != "tester" || !e.ChangedAt.Equal(f.now) {
			t.Errorf("event %+v; want an ID, changed_by tester, changed_at %v", e, f.now)
		}
	}
}

func TestUpdateLogsOnlyWhatChanged(t *testing.T) {
	f := newFixture(t)
	a := f.create(tracker.NewIssue{Title: "Same", Assignee: "bob", Labels: []string{"keep"}}).ID
	f.tick()

	// Everything here is already the issue's value, or already absent.
	got := f.update(a, tracker.Patch{
		Title: ptr("Same"), Type: ptr("task"), Priority: ptr(2), Assignee: ptr("bob"),
		Description: ptr(""), Owner: ptr(""), DueAt: &time.Time{},
		AddLabels: []string{"keep"}, RemoveLabels: []string{"absent"},
	})
	f.wantChanges(a)
	if !got.UpdatedAt.Equal(start) {
		t.Errorf("updated_at = %v after an update that changed nothing; want %v", got.UpdatedAt, start)
	}

	// One real change among the same values: one event, and never updated_at.
	got = f.update(a, tracker.Patch{Title: ptr("Same"), Priority: ptr(0)})
	f.wantChanges(a, change{"priority", 2, 0})
	if !got.UpdatedAt.Equal(f.now) {
		t.Errorf("updated_at = %v; want %v", got.UpdatedAt, f.now)
	}
}

func TestTextFieldsLogHashesNotBodies(t *testing.T) {
	f := newFixture(t)
	a := f.titled("A")

	got := f.update(a, tracker.Patch{
		Description: ptr("A long description"), Design: ptr("Use a view"),
		AcceptanceCriteria: ptr("ready stays fast"), Notes: ptr("Line\n"),
	})
	if got.Description != "A long description" || got.Design != "Use a view" ||
		got.AcceptanceCriteria != "ready stays fast" || got.Notes != "Line\n" {
		t.Errorf("texts = %q, %q, %q, %q", got.Description, got.Design, got.AcceptanceCriteria, got.Notes)
	}
	f.wantChanges(a,
		change{"description", emptyTextHash, hashOf("A long description")},
		change{"design", emptyTextHash, hashOf("Use a view")},
		change{"acceptance_criteria", emptyTextHash, hashOf("ready stays fast")},
		change{"notes", emptyTextHash, hashOf("Line\n")},
	)

	// Editing again logs the old hash; the same text again logs nothing; and
	// a text field cleared goes back to the empty text.
	f.update(a, tracker.Patch{Description: ptr("Shorter"), Design: ptr("Use a view"), Notes: ptr("")})
	if got := f.changes(a)[4:]; !reflect.DeepEqual(got, []change{
		{"description", hashOf("A long description"), hashOf("Shorter")},
		{"notes", hashOf("Line\n"), emptyTextHash},
	}) {
		t.Errorf("second edit logged %#v", got)
	}
	if got := f.get(a); got.Description != "Shorter" || got.Notes != "" {
		t.Errorf("description, notes = %q, %q", got.Description, got.Notes)
	}
}

func TestLabelsLogOneElementPerEvent(t *testing.T) {
	f := newFixture(t)
	a := f.create(tracker.NewIssue{Title: "A", Labels: []string{"keep"}}).ID

	got := f.update(a, tracker.Patch{AddLabels: []string{"b", "a", "b"}, RemoveLabels: []string{"keep"}})
	wantStrings(t, "labels", got.Labels, "a", "b")
	f.wantChanges(a,
		change{"labels", nil, "b"},
		change{"labels", nil, "a"},
		change{"labels", "keep", nil},
	)
}

func TestClearingOptionalFields(t *testing.T) {
	f := newFixture(t)
	a := f.create(tracker.NewIssue{Title: "A", Assignee: "bob"}).ID
	due := time.Date(2026, 2, 1, 12, 30, 0, 0, time.FixedZone("", 2*60*60))
	f.update(a, tracker.Patch{Owner: ptr("carol"), DueAt: &due})

	got := f.update(a, tracker.Patch{Assignee: ptr(""), Owner: ptr(""), DueAt: &time.Time{}})
	if got.Assignee != nil || got.Owner != nil || got.DueAt != nil {
		t.Errorf("assignee, owner, due_at = %v, %v, %v; want all nil", got.Assignee, got.Owner, got.DueAt)
	}
	f.wantChanges(a,
		change{"owner", nil, "carol"},
		change{"due_at", nil, "2026-02-01T10:30:00Z"}, // UTC, whatever zone it came in
		change{"assignee", "bob", nil},
		change{"owner", "carol", nil},
		change{"due_at", "2026-02-01T10:30:00Z", nil},
	)
}

func TestUpdateRefusesBadValues(t *testing.T) {
	f := newFixture(t)
	a := f.titled("A")
	for name, p := range map[string]tracker.Patch{
		"priority 9":   {Priority: ptr(9)},
		"empty title":  {Title: ptr("")},
		"unknown type": {Type: ptr("saga")},
		"empty type":   {Type: ptr("")},
		"empty label":  {AddLabels: []string{""}},
	} {
		// A valid field alongside: a refused update changes nothing at all.
		p.Owner = ptr("carol")
		_, err := f.tr.Update(f.ctx, a, p)
		if err == nil {
			t.Errorf("%s: updated", name)
			continue
		}
		wantCode(t, err, tracker.CodeInvalidArgument)
	}
	f.wantChanges(a)

	_, err := f.tr.Update(f.ctx, "t-zzzzzzzz", tracker.Patch{Title: ptr("x")})
	wantCode(t, err, tracker.CodeNotFound)
}

func TestUpdateResolvesAPartialID(t *testing.T) {
	f := newFixture(t)
	a := f.titled("A")
	if got := f.update(a[2:], tracker.Patch{Title: ptr("Renamed")}); got.ID != a || got.Title != "Renamed" {
		t.Errorf("Update by partial ID = %s %q", got.ID, got.Title)
	}
}

func TestHistoryIsOldestFirstEvenWithinOneInstant(t *testing.T) {
	f := newFixture(t)
	a := f.titled("A")
	var want []change
	prev := "A"
	// The clock doesn't move: order comes from the event IDs alone.
	for _, title := range []string{"B", "C", "D", "E", "F", "G", "H", "I"} {
		f.update(a, tracker.Patch{Title: ptr(title)})
		want = append(want, change{"title", prev, title})
		prev = title
	}
	f.wantChanges(a, want...)
}
