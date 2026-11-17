package tracker_test

import (
	"testing"

	"github.com/plongitudes/tesserae/internal/tracker"
)

func TestAppendAddsAParagraphToEachTextGiven(t *testing.T) {
	f := newFixture(t)
	id := f.create(tracker.NewIssue{
		Title: "Issue", Description: "What", Design: "How", AcceptanceCriteria: "Done when", Notes: "Seen",
	}).ID
	got := f.update(id, tracker.Patch{
		Append:      true,
		Description: ptr("more what"), Design: ptr("more how"),
		AcceptanceCriteria: ptr("and then"), Notes: ptr("seen again"),
	})
	for _, c := range []struct{ field, got, want string }{
		{"description", got.Description, "What\n\nmore what"},
		{"design", got.Design, "How\n\nmore how"},
		{"acceptance_criteria", got.AcceptanceCriteria, "Done when\n\nand then"},
		{"notes", got.Notes, "Seen\n\nseen again"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q; want %q", c.field, c.got, c.want)
		}
	}
}

func TestAppendPutsExactlyOneBlankLineBetweenOldAndNew(t *testing.T) {
	for _, c := range []struct{ name, old, add, want string }{
		{"no trailing newline", "Line", "Second", "Line\n\nSecond"},
		{"one trailing newline", "Line\n", "Second", "Line\n\nSecond"},
		{"several trailing newlines", "Line\n\n\n", "Second", "Line\n\nSecond"},
		{"the new text is verbatim", "Line", "\n  Second \n\n", "Line\n\n\n  Second \n\n"},
		{"only trailing newlines are touched", "A\n\n\nB \n", "C", "A\n\n\nB \n\nC"},
		{"an empty field takes the text", "", "First\n", "First\n"},
		{"a field of newlines counts as empty", "\n\n", "First", "First"},
		{"appending nothing changes nothing", "Line\n", "", "Line\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			id := f.create(tracker.NewIssue{Title: "Issue", Notes: c.old}).ID
			if got := f.update(id, tracker.Patch{Append: true, Notes: ptr(c.add)}).Notes; got != c.want {
				t.Errorf("notes = %q; want %q", got, c.want)
			}
		})
	}
}

func TestAppendLogsTheHashOfTheResultingText(t *testing.T) {
	f := newFixture(t)
	id := f.create(tracker.NewIssue{Title: "Issue", Notes: "Line\n"}).ID
	f.tick()
	f.update(id, tracker.Patch{Append: true, Notes: ptr("Second")})
	f.wantChanges(id, change{"notes", hashOf("Line\n"), hashOf("Line\n\nSecond")})

	// Appending nothing logs nothing and leaves updated_at alone.
	before := f.get(id).UpdatedAt
	f.tick()
	f.update(id, tracker.Patch{Append: true, Notes: ptr("")})
	f.wantChanges(id, change{"notes", hashOf("Line\n"), hashOf("Line\n\nSecond")})
	if after := f.get(id).UpdatedAt; !after.Equal(before) {
		t.Errorf("updated_at moved from %v to %v", before, after)
	}
}

func TestAppendLeavesTheOtherFieldsOfThePatchAsTheyAre(t *testing.T) {
	f := newFixture(t)
	id := f.create(tracker.NewIssue{Title: "Issue", Notes: "Line", Assignee: "alice"}).ID
	got := f.update(id, tracker.Patch{
		Append: true, Notes: ptr("Second"), Priority: ptr(0), Assignee: ptr("bob"), AddLabels: []string{"x"},
	})
	if got.Notes != "Line\n\nSecond" || got.Priority != 0 || got.Assignee == nil || *got.Assignee != "bob" {
		t.Errorf("got notes %q, priority %d, assignee %v", got.Notes, got.Priority, got.Assignee)
	}
	wantStrings(t, "labels", got.Labels, "x")
}

func TestAppendNeedsATextAndNeverTakesATitle(t *testing.T) {
	f := newFixture(t)
	id := f.create(tracker.NewIssue{Title: "Issue", Notes: "Line"}).ID
	for name, p := range map[string]tracker.Patch{
		"nothing":        {Append: true},
		"no text":        {Append: true, Priority: ptr(1)},
		"title":          {Append: true, Title: ptr("More")},
		"title and text": {Append: true, Title: ptr("More"), Notes: ptr("Second")},
	} {
		_, err := f.tr.Update(f.ctx, id, p)
		wantCode(t, err, tracker.CodeInvalidArgument)
		if err == nil {
			t.Errorf("%s: Update succeeded", name)
		}
	}
	got := f.get(id)
	if got.Title != "Issue" || got.Notes != "Line" {
		t.Errorf("a refused append changed the issue: title %q, notes %q", got.Title, got.Notes)
	}
	f.wantChanges(id)
}
