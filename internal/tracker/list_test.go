package tracker_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/plongitudes/tesserae/internal/tracker"
)

func (f *fixture) list(filter tracker.Filter) []string {
	f.t.Helper()
	issues, err := f.tr.List(f.ctx, filter)
	if err != nil {
		f.t.Fatalf("List(%+v): %v", filter, err)
	}
	if issues == nil {
		f.t.Errorf("List(%+v) returned nil; want an empty list", filter)
	}
	return issueIDs(issues)
}

// board is a small tracker with one of everything a filter can tell apart.
type board struct {
	*fixture
	epic, refresh, rotate, typo, old, dropped, held, blocker, dependent, gone string
}

func newBoard(t *testing.T) *board {
	b := &board{fixture: newFixture(t)}
	f := b.fixture
	add := func(n tracker.NewIssue) string {
		f.tick()
		return f.create(n).ID
	}
	b.epic = add(tracker.NewIssue{Title: "Auth overhaul", Type: "epic", Priority: ptr(1)})
	b.refresh = add(tracker.NewIssue{Title: "Add token refresh", Parent: b.epic, Priority: ptr(1), Labels: []string{"auth", "api"}, Assignee: "alice"})
	b.rotate = add(tracker.NewIssue{Title: "Rotate signing keys", Parent: b.refresh, Labels: []string{"auth"}, Notes: "Ask Bob about the HSM"})
	b.typo = add(tracker.NewIssue{Title: "Fix README typo", Type: "chore", Priority: ptr(3), Description: "It mentions TOKENS in the body"})
	b.old = add(tracker.NewIssue{Title: "Old work", Priority: ptr(0), Assignee: "alice"})
	b.dropped = add(tracker.NewIssue{Title: "Dropped work", Priority: ptr(4)})
	b.held = add(tracker.NewIssue{Title: "On hold", Type: "bug"})
	b.blocker = add(tracker.NewIssue{Title: "Blocker", Type: "bug", Priority: ptr(0)})
	b.dependent = add(tracker.NewIssue{Title: "Dependent", BlockedBy: []string{b.blocker}, Labels: []string{"api"}})
	b.gone = add(tracker.NewIssue{Title: "A mistake about tokens", Type: "chore", Labels: []string{"auth"}, Assignee: "alice", Parent: b.epic})
	f.tick()
	f.setStatus(b.old, "done")
	f.setStatus(b.dropped, "omit")
	f.setStatus(b.held, "held")
	f.setStatus(b.blocker, "in-progress")
	f.retire(b.gone)
	return b
}

func TestListDefaultsToOpenIssuesInListOrder(t *testing.T) {
	b := newBoard(t)
	// Priority first, then created_at; done, omit and retired issues are out.
	wantStrings(t, "list", b.list(tracker.Filter{}),
		b.blocker, b.epic, b.refresh, b.rotate, b.held, b.dependent, b.typo)
}

func TestListOrdersByPriorityThenCreatedAtThenID(t *testing.T) {
	f := newFixture(t)
	// Three made in the same instant differ only by ID.
	same := []string{f.titled("One"), f.titled("Two"), f.titled("Three")}
	byID := append([]string(nil), same...)
	slices.Sort(byID)
	f.tick()
	later := f.titled("Later")
	f.tick()
	urgent := f.create(tracker.NewIssue{Title: "Urgent", Priority: ptr(0)}).ID
	// An earlier created_at wins over a lower ID.
	f.now = start.Add(-time.Hour)
	earliest := f.titled("Earliest")

	wantStrings(t, "list", f.list(tracker.Filter{}), urgent, earliest, byID[0], byID[1], byID[2], later)
}

func TestListAllIncludesDoneAndOmitButNeverRetired(t *testing.T) {
	b := newBoard(t)
	wantStrings(t, "list --all", b.list(tracker.Filter{All: true}),
		b.old, b.blocker, b.epic, b.refresh, b.rotate, b.held, b.dependent, b.typo, b.dropped)
}

func TestListRetiredShowsOnlyRetiredIssues(t *testing.T) {
	b := newBoard(t)
	wantStrings(t, "list --retired", b.list(tracker.Filter{Retired: true}), b.gone)
	// A retired done issue is listed too: retired is not a status.
	b.retire(b.old)
	wantStrings(t, "list --retired", b.list(tracker.Filter{Retired: true}), b.old, b.gone)
	wantStrings(t, "list --retired --all", b.list(tracker.Filter{Retired: true, All: true}), b.old, b.gone)
	wantStrings(t, "list --retired --type chore", b.list(tracker.Filter{Retired: true, Type: "chore"}), b.gone)
}

func TestRetiredIssuesAreAbsentFromEveryOtherList(t *testing.T) {
	b := newBoard(t)
	// b.gone would match each of these if it were live.
	filters := map[string]tracker.Filter{
		"default":  {},
		"all":      {All: true},
		"status":   {Status: "ready"},
		"ready":    {Ready: true},
		"type":     {Type: "chore"},
		"label":    {Label: "auth"},
		"assignee": {Assignee: "alice", All: true},
		"parent":   {Parent: b.epic},
		"search":   {Search: "tokens"},
		"blocked":  {Blocked: true, All: true},
	}
	for name, filter := range filters {
		issues, err := b.tr.List(b.ctx, filter)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, i := range issues {
			if i.ID == b.gone || i.RetiredAt != nil {
				t.Errorf("list by %s shows the retired issue %s", name, i.ID)
			}
		}
	}
	// Restored, it is back.
	b.restore(b.gone)
	wantStrings(t, "list --type chore", b.list(tracker.Filter{Type: "chore"}), b.gone, b.typo)
}

func TestListFiltersByDisplayedStatus(t *testing.T) {
	b := newBoard(t)
	// The epic and the refresh task have open children; the dependent is blocked.
	wantStrings(t, "waiting", b.list(tracker.Filter{Status: "waiting"}), b.epic, b.refresh, b.dependent)
	wantStrings(t, "ready", b.list(tracker.Filter{Status: "ready"}), b.rotate, b.typo)
	wantStrings(t, "in-progress", b.list(tracker.Filter{Status: "in-progress"}), b.blocker)
	wantStrings(t, "held", b.list(tracker.Filter{Status: "held"}), b.held)
	// Asking for done or omit by name doesn't need All.
	wantStrings(t, "done", b.list(tracker.Filter{Status: "done"}), b.old)
	wantStrings(t, "omit", b.list(tracker.Filter{Status: "omit"}), b.dropped)
	wantStrings(t, "pending-review", b.list(tracker.Filter{Status: "pending-review"}))

	_, err := b.tr.List(b.ctx, tracker.Filter{Status: "open"})
	wantCode(t, err, tracker.CodeInvalidStatus)
}

func TestReadyIsListWithTheReadyFilter(t *testing.T) {
	b := newBoard(t)
	wantStrings(t, "ready", b.list(tracker.Filter{Ready: true}), b.rotate, b.typo)
	issues, err := b.tr.List(b.ctx, tracker.Filter{Ready: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range issues {
		if i.Status != "ready" || i.Blocked {
			t.Errorf("%s in the ready list reads as status %s, blocked %v", i.ID, i.Status, i.Blocked)
		}
	}

	// Finishing the blocker makes its dependent ready; finishing the last
	// child makes the parent ready.
	b.setStatus(b.blocker, "done")
	b.setStatus(b.rotate, "done")
	wantStrings(t, "ready", b.list(tracker.Filter{Ready: true}), b.refresh, b.dependent, b.typo)
	// A held ancestor takes its descendants out of the ready list.
	b.setStatus(b.epic, "held")
	wantStrings(t, "ready", b.list(tracker.Filter{Ready: true}), b.dependent, b.typo)
	wantStrings(t, "ready -n 1", b.list(tracker.Filter{Ready: true, Limit: 1}), b.dependent)
}

func TestListFiltersByBlocked(t *testing.T) {
	b := newBoard(t)
	wantStrings(t, "blocked", b.list(tracker.Filter{Blocked: true}), b.dependent)
	// Open children make an issue waiting, not blocked; a held ancestor
	// blocks every descendant, whatever its status.
	b.setStatus(b.rotate, "in-progress")
	b.setStatus(b.epic, "held")
	wantStrings(t, "blocked", b.list(tracker.Filter{Blocked: true}), b.refresh, b.rotate, b.dependent)
	issues, err := b.tr.List(b.ctx, tracker.Filter{Blocked: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range issues {
		if !i.Blocked {
			t.Errorf("%s in the blocked list reads as not blocked", i.ID)
		}
	}
	wantStrings(t, "blocked and in-progress", b.list(tracker.Filter{Blocked: true, Status: "in-progress"}), b.rotate)
}

func TestListFiltersByTypeLabelAndAssignee(t *testing.T) {
	b := newBoard(t)
	wantStrings(t, "type bug", b.list(tracker.Filter{Type: "bug"}), b.blocker, b.held)
	wantStrings(t, "type chore", b.list(tracker.Filter{Type: "chore"}), b.typo)
	wantStrings(t, "label auth", b.list(tracker.Filter{Label: "auth"}), b.refresh, b.rotate)
	wantStrings(t, "label api", b.list(tracker.Filter{Label: "api"}), b.refresh, b.dependent)
	wantStrings(t, "label none", b.list(tracker.Filter{Label: "nope"}))
	wantStrings(t, "assignee alice", b.list(tracker.Filter{Assignee: "alice"}), b.refresh)
	wantStrings(t, "assignee alice, all", b.list(tracker.Filter{Assignee: "alice", All: true}), b.old, b.refresh)
	// Filters combine with AND.
	wantStrings(t, "label api, assignee alice", b.list(tracker.Filter{Label: "api", Assignee: "alice"}), b.refresh)

	_, err := b.tr.List(b.ctx, tracker.Filter{Type: "story"})
	wantCode(t, err, tracker.CodeInvalidArgument)
}

func TestListFiltersByParentToAllDescendants(t *testing.T) {
	b := newBoard(t)
	wantStrings(t, "parent epic", b.list(tracker.Filter{Parent: b.epic}), b.refresh, b.rotate)
	wantStrings(t, "parent refresh", b.list(tracker.Filter{Parent: b.refresh[2:]}), b.rotate)
	wantStrings(t, "parent leaf", b.list(tracker.Filter{Parent: b.rotate}))
	// The hierarchy rides on each issue.
	issues, err := b.tr.List(b.ctx, tracker.Filter{Parent: b.epic})
	if err != nil {
		t.Fatal(err)
	}
	deep := issues[1]
	if deep.Parent == nil || *deep.Parent != b.refresh {
		t.Errorf("parent = %v; want %s", deep.Parent, b.refresh)
	}
	wantStrings(t, "ancestors", deep.Ancestors, b.refresh, b.epic)
	wantStrings(t, "labels", issues[0].Labels, "api", "auth")

	_, err = b.tr.List(b.ctx, tracker.Filter{Parent: "nope"})
	wantCode(t, err, tracker.CodeNotFound)
}

func TestListSearchesTitleDescriptionAndNotes(t *testing.T) {
	b := newBoard(t)
	wantStrings(t, "title", b.list(tracker.Filter{Search: "signing"}), b.rotate)
	wantStrings(t, "title, other case", b.list(tracker.Filter{Search: "SIGNING k"}), b.rotate)
	wantStrings(t, "title and description", b.list(tracker.Filter{Search: "token"}), b.refresh, b.typo)
	wantStrings(t, "description", b.list(tracker.Filter{Search: "in the BODY"}), b.typo)
	wantStrings(t, "notes", b.list(tracker.Filter{Search: "hsm"}), b.rotate)
	wantStrings(t, "nothing", b.list(tracker.Filter{Search: "kerberos"}))
	// The pattern is literal text, not a wildcard.
	wantStrings(t, "percent", b.list(tracker.Filter{Search: "%"}))
	wantStrings(t, "underscore", b.list(tracker.Filter{Search: "to_en"}))
	// Design and acceptance criteria aren't searched.
	b.update(b.typo, tracker.Patch{Design: ptr("uses kerberos")})
	wantStrings(t, "design", b.list(tracker.Filter{Search: "kerberos"}))
}

func TestListSearchMatchesIDs(t *testing.T) {
	b := newBoard(t)
	hash := b.rotate[strings.Index(b.rotate, "-")+1:]
	wantStrings(t, "whole ID", b.list(tracker.Filter{Search: b.rotate}), b.rotate)
	wantStrings(t, "whole ID, other case", b.list(tracker.Filter{Search: strings.ToUpper(b.rotate)}), b.rotate)
	if got := b.list(tracker.Filter{Search: hash}); !slices.Contains(got, b.rotate) {
		t.Errorf("search for %q = %v; want it to hold %s", hash, got, b.rotate)
	}
	// The other filters still apply: a retired issue is found only with Retired.
	wantStrings(t, "retired, by ID", b.list(tracker.Filter{Search: b.gone}))
	wantStrings(t, "retired, asked for", b.list(tracker.Filter{Search: b.gone, Retired: true}), b.gone)
}

func TestListLimitCountsFromTheTop(t *testing.T) {
	b := newBoard(t)
	wantStrings(t, "limit 2", b.list(tracker.Filter{Limit: 2}), b.blocker, b.epic)
	wantStrings(t, "limit 100", b.list(tracker.Filter{Limit: 100}),
		b.blocker, b.epic, b.refresh, b.rotate, b.held, b.dependent, b.typo)
	wantStrings(t, "limit after filter", b.list(tracker.Filter{Label: "auth", Limit: 1}), b.refresh)
	_, err := b.tr.List(b.ctx, tracker.Filter{Limit: -1})
	wantCode(t, err, tracker.CodeInvalidArgument)
}

func TestListedIssuesReadAsGetReadsThem(t *testing.T) {
	b := newBoard(t)
	issues, err := b.tr.List(b.ctx, tracker.Filter{All: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, listed := range issues {
		got := b.get(listed.ID)
		got.Links, got.Comments, got.PreviousStatus = nil, nil, nil
		if !reflect.DeepEqual(listed, got) {
			t.Errorf("listed %s:\n got  %+v\n want %+v", listed.ID, listed, got)
		}
		if listed.Links != nil || listed.Comments != nil || listed.History != nil {
			t.Errorf("listed %s carries show's detail", listed.ID)
		}
	}
}

func TestActiveBlockersNamesWhatHoldsEachBlockedIssueUp(t *testing.T) {
	f := newFixture(t)
	add := func(n tracker.NewIssue) string {
		f.tick()
		return f.create(n).ID
	}
	low := add(tracker.NewIssue{Title: "Low blocker", Priority: ptr(3)})
	high := add(tracker.NewIssue{Title: "High blocker", Priority: ptr(0)})
	finished := add(tracker.NewIssue{Title: "Finished blocker"})
	gone := add(tracker.NewIssue{Title: "Retired blocker"})
	parent := add(tracker.NewIssue{Title: "Parent", BlockedBy: []string{low, high, finished, gone}})
	child := add(tracker.NewIssue{Title: "Child", Parent: parent})
	freed := add(tracker.NewIssue{Title: "Freed", BlockedBy: []string{finished}})
	f.setStatus(finished, "done")
	f.retire(gone)

	got, err := f.tr.ActiveBlockers(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Blockers come in list order; a done or retired blocker isn't one. The
	// child is blocked through its parent and has no blocker of its own.
	want := map[string][]string{parent: {high, low}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ActiveBlockers = %v, want %v (child %s, freed %s)", got, want, child, freed)
	}
	// The same issues, and no others, are what the blocked list starts from.
	wantStrings(t, "blocked", f.list(tracker.Filter{Blocked: true}), parent, child)
}
