package tracker_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/plongitudes/tesserae/internal/tracker"
)

func (f *fixture) status(status, reason string, ids ...string) tracker.Change {
	f.t.Helper()
	c, err := f.tr.Status(f.ctx, ids, status, reason, false)
	if err != nil {
		f.t.Fatalf("Status(%v, %q, %q): %v", ids, status, reason, err)
	}
	return c
}

func issueIDs(issues []tracker.Issue) []string {
	out := []string{}
	for _, i := range issues {
		out = append(out, i.ID)
	}
	return out
}

func TestStatusSetsEachSettableStatus(t *testing.T) {
	f := newFixture(t)
	a := f.titled("A")
	for _, s := range []string{"in-progress", "pending-review", "done", "ready"} {
		c := f.status(s, "", a)
		if len(c.Updated) != 1 || c.Updated[0].ID != a || c.Updated[0].Status != s {
			t.Errorf("Status(%s) updated = %+v", s, c.Updated)
		}
		if c.NewlyUnblocked == nil || len(c.NewlyUnblocked) != 0 {
			t.Errorf("Status(%s) newly unblocked = %#v; want an empty list", s, c.NewlyUnblocked)
		}
	}
	for _, s := range []string{"held", "omit"} {
		b := f.titled("B " + s)
		c := f.status(s, "because", b)
		if got := c.Updated[0]; got.Status != s || got.StatusReason == nil || *got.StatusReason != "because" {
			t.Errorf("Status(%s) = %s, reason %v", s, got.Status, got.StatusReason)
		}
	}
}

func TestStatusLogsStatusAndReasonAndNothingElse(t *testing.T) {
	f := newFixture(t)
	a := f.create(tracker.NewIssue{Title: "A", Assignee: "alice", Labels: []string{"x"}}).ID
	before := f.get(a)

	f.tick()
	f.status("in-progress", "", a)
	f.status("held", "waiting on vendor", a)
	f.status("in-progress", "", a)
	f.wantChanges(a,
		change{"status", "ready", "in-progress"},
		change{"status", "in-progress", "held"},
		change{"status_reason", nil, "waiting on vendor"},
		change{"status", "held", "in-progress"},
		change{"status_reason", "waiting on vendor", nil},
	)

	after := f.get(a)
	if after.Assignee == nil || *after.Assignee != "alice" {
		t.Errorf("assignee = %v; a status change must leave it alone", after.Assignee)
	}
	wantStrings(t, "labels", after.Labels, "x")
	if after.Title != before.Title || after.Priority != before.Priority || after.Owner != nil {
		t.Errorf("other fields changed: %+v", after)
	}
	if !after.UpdatedAt.Equal(f.now) {
		t.Errorf("updated_at = %v; want %v", after.UpdatedAt, f.now)
	}
}

func TestSettingTheSameStatusAgainChangesNothing(t *testing.T) {
	f := newFixture(t)
	a := f.titled("A")
	f.status("done", "shipped in v2", a)
	f.tick()
	c := f.status("done", "", a)
	if got := c.Updated[0]; got.StatusReason == nil || *got.StatusReason != "shipped in v2" || !got.UpdatedAt.Equal(start) {
		t.Errorf("reason %v, updated_at %v; want both untouched", got.StatusReason, got.UpdatedAt)
	}
	// A new reason for the same status replaces the old one.
	f.status("done", "shipped in v3", a)
	f.wantChanges(a,
		change{"status", "ready", "done"},
		change{"status_reason", nil, "shipped in v2"},
		change{"status_reason", "shipped in v2", "shipped in v3"},
	)
}

func TestStatusRefusesUnsettableStatuses(t *testing.T) {
	f := newFixture(t)
	a := f.titled("A")
	for _, s := range []string{"waiting", "closed", "", "Done"} {
		_, err := f.tr.Status(f.ctx, []string{a}, s, "", false)
		wantCode(t, err, "invalid_status")
	}
	f.wantChanges(a)
}

func TestHeldAndOmitNeedAReason(t *testing.T) {
	f := newFixture(t)
	a := f.titled("A")
	for _, s := range []string{"held", "omit"} {
		for _, reason := range []string{"", "  "} {
			_, err := f.tr.Status(f.ctx, []string{a}, s, reason, false)
			wantCode(t, err, "reason_required")
		}
	}
	if got := f.get(a); got.Status != "ready" {
		t.Errorf("status = %s; want ready", got.Status)
	}
}

func TestADoneOrOmittedIssueCantBeHeld(t *testing.T) {
	f := newFixture(t)
	done, omitted := f.titled("Done"), f.titled("Omitted")
	f.status("done", "", done)
	f.status("omit", "dropped", omitted)
	for _, id := range []string{done, omitted} {
		_, err := f.tr.Status(f.ctx, []string{id}, "held", "x", false)
		wantCode(t, err, "invalid_transition")
	}
	// They can come back to not started or straight to in-progress (ADR-0009).
	f.status("ready", "", done)
	f.status("in-progress", "", omitted)
	if got := f.get(omitted); got.Status != "in-progress" || got.StatusReason != nil {
		t.Errorf("reopened: status %s, reason %v; want in-progress and no reason", got.Status, got.StatusReason)
	}
}

func TestAParentWithOpenChildrenCantBeDoneOrOmitted(t *testing.T) {
	f := newFixture(t)
	p := f.titled("Parent")
	c1 := f.create(tracker.NewIssue{Title: "Child 1", Parent: p}).ID
	c2 := f.create(tracker.NewIssue{Title: "Child 2", Parent: p}).ID
	grand := f.create(tracker.NewIssue{Title: "Grandchild", Parent: c1}).ID

	_, err := f.tr.Status(f.ctx, []string{p}, "done", "", true)
	wantCode(t, err, "has_open_children") // --force doesn't lift this one (ADR-0016)
	var te *tracker.Error
	if errors.As(err, &te) {
		for _, id := range []string{c1, c2, grand} {
			if !strings.Contains(te.Message, id) {
				t.Errorf("message %q doesn't list open child %s", te.Message, id)
			}
		}
	}
	_, err = f.tr.Status(f.ctx, []string{p}, "omit", "x", false)
	wantCode(t, err, "has_open_children")

	f.status("done", "", grand)
	f.status("done", "", c1)
	f.status("omit", "dropped", c2)
	if got := f.status("done", "", p).Updated[0]; got.Status != "done" {
		t.Errorf("parent status = %s; want done", got.Status)
	}
}

func TestDoneIsRefusedWhileBlockedUnlessForced(t *testing.T) {
	f := newFixture(t)
	blocker := f.titled("Blocker")
	dep := f.create(tracker.NewIssue{Title: "Dependent", BlockedBy: []string{blocker}}).ID
	epic := f.titled("Epic")
	child := f.create(tracker.NewIssue{Title: "Child", Parent: epic}).ID
	f.status("held", "paused", epic)

	for _, id := range []string{dep, child} {
		_, err := f.tr.Status(f.ctx, []string{id}, "done", "", false)
		wantCode(t, err, "blocked")
		if got := f.get(id); got.Status != "waiting" {
			t.Errorf("%s status = %s; want waiting", id, got.Status)
		}
	}

	c, err := f.tr.Status(f.ctx, []string{dep}, "done", "", true)
	if err != nil {
		t.Fatalf("forced done: %v", err)
	}
	if c.Updated[0].Status != "done" {
		t.Errorf("forced done: status = %s", c.Updated[0].Status)
	}
	// Only done is guarded: a blocked issue can be started, held, or omitted.
	f.status("in-progress", "", child)
	f.status("omit", "not needed", child)
}

func TestStatusTakesSeveralIDs(t *testing.T) {
	f := newFixture(t)
	a, b := f.titled("A"), f.titled("B")
	c := f.status("in-progress", "", b, a[2:], b)
	wantStrings(t, "updated", issueIDs(c.Updated), b, a)
	for _, id := range []string{a, b} {
		if got := f.get(id); got.Status != "in-progress" {
			t.Errorf("%s status = %s; want in-progress", id, got.Status)
		}
	}

	_, err := f.tr.Status(f.ctx, nil, "done", "", false)
	wantCode(t, err, "invalid_argument")
}

func TestStatusOverSeveralIDsIsAllOrNothing(t *testing.T) {
	f := newFixture(t)
	a, parent := f.titled("A"), f.titled("Parent")
	f.create(tracker.NewIssue{Title: "Child", Parent: parent})

	_, err := f.tr.Status(f.ctx, []string{a, parent}, "done", "", false)
	wantCode(t, err, "has_open_children")
	if got := f.get(a); got.Status != "ready" {
		t.Errorf("A status = %s; a refused command must change nothing", got.Status)
	}
	f.wantChanges(a)

	_, err = f.tr.Status(f.ctx, []string{a, "nope"}, "done", "", false)
	wantCode(t, err, "not_found")
}

func TestSeveralIDsApplyInTheOrderGiven(t *testing.T) {
	f := newFixture(t)
	blocker := f.titled("Blocker")
	dep := f.create(tracker.NewIssue{Title: "Dependent", BlockedBy: []string{blocker}}).ID

	_, err := f.tr.Status(f.ctx, []string{dep, blocker}, "done", "", false)
	wantCode(t, err, "blocked")
	c := f.status("done", "", blocker, dep)
	wantStrings(t, "updated", issueIDs(c.Updated), blocker, dep)
}

func TestStatusRefusesARetiredIssue(t *testing.T) {
	f := newFixture(t)
	a := f.titled("A")
	f.retire(a)
	_, err := f.tr.Status(f.ctx, []string{a}, "done", "", false)
	wantCode(t, err, "retired")
}

func TestDoneAndOmitReportTheDependentsTheyUnblock(t *testing.T) {
	for _, tc := range []struct{ status, reason string }{{"done", ""}, {"omit", "dropped"}} {
		t.Run(tc.status, func(t *testing.T) {
			f := newFixture(t)
			blocker, other := f.titled("Blocker"), f.titled("Other blocker")
			dep := f.create(tracker.NewIssue{Title: "Dependent", BlockedBy: []string{blocker}}).ID
			still := f.create(tracker.NewIssue{Title: "Still blocked", BlockedBy: []string{blocker, other}}).ID
			f.titled("Bystander")

			c := f.status(tc.status, tc.reason, blocker)
			wantStrings(t, "newly unblocked", issueIDs(c.NewlyUnblocked), dep)
			if got := c.NewlyUnblocked[0]; got.Status != "ready" || got.Blocked {
				t.Errorf("unblocked dependent: status %s, blocked %v; want ready, false", got.Status, got.Blocked)
			}
			if got := f.get(still); !got.Blocked {
				t.Errorf("%s has another active blocker and should stay blocked", still)
			}

			// Reopening the blocker blocks again; nothing is newly unblocked.
			c = f.status("ready", "", blocker)
			wantStrings(t, "newly unblocked on reopen", issueIDs(c.NewlyUnblocked))
			if got := f.get(dep); got.Status != "waiting" {
				t.Errorf("dependent after reopen = %s; want waiting", got.Status)
			}
		})
	}
}

func TestNewlyUnblockedReachesDescendantsInListOrder(t *testing.T) {
	f := newFixture(t)
	blocker := f.titled("Blocker")
	f.tick()
	epic := f.create(tracker.NewIssue{Title: "Epic", BlockedBy: []string{blocker}}).ID
	f.tick()
	low := f.create(tracker.NewIssue{Title: "Low child", Parent: epic, Priority: ptr(3)}).ID
	f.tick()
	high := f.create(tracker.NewIssue{Title: "High child", Parent: epic, Priority: ptr(0)}).ID

	c := f.status("done", "", blocker)
	// Priority first, then creation time: the epic (2) sits between its children.
	wantStrings(t, "newly unblocked", issueIDs(c.NewlyUnblocked), high, epic, low)
}

func TestEndingAHoldUnblocksTheDescendants(t *testing.T) {
	f := newFixture(t)
	epic := f.titled("Epic")
	child := f.create(tracker.NewIssue{Title: "Child", Parent: epic}).ID
	if c := f.status("held", "paused", epic); len(c.NewlyUnblocked) != 0 {
		t.Errorf("holding unblocked %v", issueIDs(c.NewlyUnblocked))
	}
	c := f.status("ready", "", epic)
	wantStrings(t, "newly unblocked", issueIDs(c.NewlyUnblocked), child)
}
