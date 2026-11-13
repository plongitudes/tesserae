package tracker_test

import (
	"regexp"
	"testing"
	"time"

	"github.com/plongitudes/tesserae/internal/tracker"
)

func (f *fixture) comment(id, text string) tracker.Comment {
	f.t.Helper()
	c, err := f.tr.Comment(f.ctx, id, text)
	if err != nil {
		f.t.Fatalf("Comment(%q, %q): %v", id, text, err)
	}
	return c
}

func (f *fixture) restore(id string) tracker.Issue {
	f.t.Helper()
	issue, err := f.tr.Restore(f.ctx, id)
	if err != nil {
		f.t.Fatalf("Restore(%q): %v", id, err)
	}
	return issue
}

func TestRetireSetsRetiredAtAndLogsIt(t *testing.T) {
	f := newFixture(t)
	a := f.titled("Mistake")
	f.now = start.Add(48 * time.Hour)
	c := f.retire(a[2:]) // a partial ID reaches a live issue

	wantStrings(t, "updated", issueIDs(c.Updated), a)
	got := c.Updated[0]
	if got.RetiredAt == nil || !got.RetiredAt.Equal(f.now) {
		t.Errorf("retired_at = %v; want %v", got.RetiredAt, f.now)
	}
	if !got.UpdatedAt.Equal(f.now) {
		t.Errorf("updated_at = %v; want %v", got.UpdatedAt, f.now)
	}
	if got.Status != "ready" || got.StatusReason != nil {
		t.Errorf("retiring changed the status: %s, reason %v", got.Status, got.StatusReason)
	}
	if c.NewlyUnblocked == nil || len(c.NewlyUnblocked) != 0 {
		t.Errorf("newly_unblocked = %#v; want empty, not nil", c.NewlyUnblocked)
	}
	f.wantChanges(a, change{"retired_at", nil, "2026-01-03T00:00:00Z"})
	if got := f.get(a).Comments; len(got) != 0 {
		t.Errorf("retiring without a reason left comments: %+v", got)
	}
}

func TestRetireKeepsItsReasonAsAComment(t *testing.T) {
	f := newFixture(t)
	a := f.titled("Mistake")
	if _, err := f.tr.Retire(f.ctx, []string{a}, "created by accident"); err != nil {
		t.Fatal(err)
	}
	got := f.get(a)
	if len(got.Comments) != 1 || got.Comments[0].Text != "Retired: created by accident" || got.Comments[0].Author != "tester" {
		t.Errorf("comments = %+v; want the reason, by tester", got.Comments)
	}
	if got.StatusReason != nil {
		t.Errorf("status_reason = %q; the retire reason is not a status reason", *got.StatusReason)
	}
}

func TestRetireReportsNewlyUnblockedIssues(t *testing.T) {
	f := newFixture(t)
	blocker := f.titled("Mistake")
	dep := f.create(tracker.NewIssue{Title: "Dependent", BlockedBy: []string{blocker}}).ID
	other := f.titled("Other blocker")
	still := f.create(tracker.NewIssue{Title: "Still blocked", BlockedBy: []string{blocker, other}}).ID
	// A held parent blocks its child; retiring the parent frees the child.
	parent := f.titled("Held parent")
	child := f.create(tracker.NewIssue{Title: "Child", Parent: parent}).ID
	f.setStatus(parent, "held")
	// A blocked issue that is retired itself is not "unblocked".
	f.link(blocker, "blocks", parent)

	c := f.retire(blocker, parent)
	wantStrings(t, "updated", issueIDs(c.Updated), blocker, parent)
	wantStrings(t, "newly_unblocked", issueIDs(c.NewlyUnblocked), dep, child)
	for _, u := range c.NewlyUnblocked {
		if u.Blocked || u.Status != "ready" {
			t.Errorf("%s reads as status %s, blocked %v after its blocker was retired", u.ID, u.Status, u.Blocked)
		}
	}
	if got := f.get(still); !got.Blocked {
		t.Errorf("%s has a live blocker left and should still be blocked", still)
	}
	if got := f.get(child); got.Parent != nil || len(got.Ancestors) != 0 {
		t.Errorf("child of a retired parent: parent %v, ancestors %v; want none", got.Parent, got.Ancestors)
	}
}

func TestRetireIsAllOrNothingAndRefusesARetiredIssue(t *testing.T) {
	f := newFixture(t)
	a, b := f.titled("A"), f.titled("B")
	f.retire(b)

	_, err := f.tr.Retire(f.ctx, []string{a, b}, "")
	wantCode(t, err, tracker.CodeRetired)
	if f.get(a).RetiredAt != nil {
		t.Errorf("A was retired by a command that failed")
	}
	_, err = f.tr.Retire(f.ctx, []string{a, "nope"}, "")
	wantCode(t, err, tracker.CodeNotFound)
	_, err = f.tr.Retire(f.ctx, nil, "")
	wantCode(t, err, tracker.CodeInvalidArgument)

	// Naming an issue twice retires it once.
	c := f.retire(a, a)
	wantStrings(t, "updated", issueIDs(c.Updated), a)
	f.wantChanges(a, change{"retired_at", nil, "2026-01-01T00:00:00Z"})
}

func TestARetiredIssueIsReachedOnlyByItsExactID(t *testing.T) {
	f := newFixture(t)
	a := f.titled("Mistake")
	f.retire(a)

	if got := f.get(a); got.RetiredAt == nil {
		t.Errorf("Get by exact ID: retired_at is null")
	}
	_, err := f.tr.Get(f.ctx, a[2:])
	wantCode(t, err, tracker.CodeNotFound)
	_, err = f.tr.Comment(f.ctx, a, "too late")
	wantCode(t, err, tracker.CodeRetired)
	_, err = f.tr.Status(f.ctx, []string{a}, "done", "", false)
	wantCode(t, err, tracker.CodeRetired)
}

func TestRestoreBringsAnIssueBackWithItsLinks(t *testing.T) {
	f := newFixture(t)
	blocker := f.titled("Mistake")
	dep := f.create(tracker.NewIssue{Title: "Dependent", BlockedBy: []string{blocker}}).ID
	parent := f.titled("Parent")
	f.link(parent, "parent-of", blocker)
	f.retire(blocker)
	if got := f.get(dep); got.Blocked {
		t.Fatalf("dependent still blocked after its blocker was retired")
	}

	f.now = start.Add(time.Hour)
	got := f.restore(blocker)
	if got.RetiredAt != nil {
		t.Errorf("retired_at = %v after restore", got.RetiredAt)
	}
	if got.Parent == nil || *got.Parent != parent {
		t.Errorf("parent after restore = %v; want %s", got.Parent, parent)
	}
	if !got.UpdatedAt.Equal(f.now) {
		t.Errorf("updated_at = %v; want %v", got.UpdatedAt, f.now)
	}
	d := f.get(dep)
	if !d.Blocked || d.Status != "waiting" {
		t.Errorf("dependent after restore: status %s, blocked %v; want waiting, true", d.Status, d.Blocked)
	}
	wantStrings(t, "blocked_by", d.Links.BlockedBy, blocker)
	f.wantChanges(blocker,
		change{"parent", nil, parent},
		change{"retired_at", nil, "2026-01-01T00:00:00Z"},
		change{"retired_at", "2026-01-01T00:00:00Z", nil})
	// It resolves by a partial ID again.
	if f.get(blocker[2:]).ID != blocker {
		t.Errorf("a restored issue doesn't resolve by partial ID")
	}
}

func TestRestoreTakesTheExactIDOnly(t *testing.T) {
	f := newFixture(t)
	a := f.titled("Mistake")
	f.retire(a)

	_, err := f.tr.Restore(f.ctx, a[2:])
	wantCode(t, err, tracker.CodeNotFound)
	_, err = f.tr.Restore(f.ctx, a[:len(a)-1])
	wantCode(t, err, tracker.CodeNotFound)
	if f.get(a).RetiredAt == nil {
		t.Errorf("a partial ID restored the issue")
	}

	// Restoring a live issue changes nothing.
	live := f.titled("Live")
	f.now = start.Add(time.Hour)
	got := f.restore(live)
	if got.ID != live || !got.UpdatedAt.Equal(start) {
		t.Errorf("restore of a live issue: %s updated_at %v; want it untouched", got.ID, got.UpdatedAt)
	}
	f.wantChanges(live)
	// A partial ID is refused for a live issue too.
	_, err = f.tr.Restore(f.ctx, live[2:])
	wantCode(t, err, tracker.CodeNotFound)
}

func TestCommentIsARecordByTheActor(t *testing.T) {
	f := newFixture(t)
	a := f.titled("A")
	f.now = start.Add(24 * time.Hour)
	c := f.comment(a[2:], "Looks good\n")

	if !regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`).MatchString(c.ID) {
		t.Errorf("comment ID %q is not a ULID", c.ID)
	}
	if c.Author != "tester" || c.Text != "Looks good\n" || !c.CreatedAt.Equal(f.now) {
		t.Errorf("comment = %+v; want tester's verbatim text at %v", c, f.now)
	}
	got := f.get(a)
	if len(got.Comments) != 1 || got.Comments[0] != c {
		t.Errorf("Get comments = %+v; want [%+v]", got.Comments, c)
	}
	// A comment is not an event and doesn't touch the issue.
	f.wantChanges(a)
	if !got.UpdatedAt.Equal(start) {
		t.Errorf("updated_at = %v; a comment should leave it at %v", got.UpdatedAt, start)
	}
}

func TestCommentsReadInTheOrderTheyWereMade(t *testing.T) {
	f := newFixture(t)
	a := f.titled("A")
	// The clock stands still for the first three, as it does under TESS_NOW.
	want := []string{"one", "two", "three"}
	for _, text := range want {
		f.comment(a, text)
	}
	f.tick()
	f.comment(a, "four")
	var got []string
	for _, c := range f.get(a).Comments {
		got = append(got, c.Text)
	}
	wantStrings(t, "comments", got, "one", "two", "three", "four")
}

func TestCommentRefusals(t *testing.T) {
	f := newFixture(t)
	a := f.titled("A")
	_, err := f.tr.Comment(f.ctx, a, "  \n")
	wantCode(t, err, tracker.CodeInvalidArgument)
	_, err = f.tr.Comment(f.ctx, "nope", "hello")
	wantCode(t, err, tracker.CodeNotFound)
	if got := f.get(a).Comments; len(got) != 0 {
		t.Errorf("comments = %+v; want none", got)
	}
}
