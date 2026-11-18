package tracker_test

import (
	"strings"
	"testing"
	"time"

	"github.com/plongitudes/tesserae/internal/tracker"
)

// importLines imports bd JSONL given as one record per argument.
func (f *fixture) importLines(lines ...string) (tracker.ImportResult, error) {
	f.t.Helper()
	return f.tr.Import(f.ctx, strings.NewReader(strings.Join(lines, "\n")+"\n"))
}

func (f *fixture) mustImport(lines ...string) tracker.ImportResult {
	f.t.Helper()
	res, err := f.importLines(lines...)
	if err != nil {
		f.t.Fatalf("Import: %v", err)
	}
	return res
}

// bd is a minimal bd record: an open task with the given ID, plus any extra
// JSON members (each starting with a comma).
func bd(id, extra string) string {
	return `{"id":"` + id + `","title":"Issue ` + id + `","status":"open","priority":2,"issue_type":"task",` +
		`"created_at":"2025-06-01T10:00:00Z","created_by":"olduser","updated_at":"2025-06-02T10:00:00Z"` + extra + `}`
}

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestImportKeepsTheIDFieldsTimestampsLabelsAndComments(t *testing.T) {
	f := newFixture(t)
	res := f.mustImport(`{"id":"bd-a1.1","title":"Old epic","description":"The description","design":"The design",` +
		`"acceptance_criteria":"The criteria","notes":"The notes","status":"open","priority":1,"issue_type":"epic",` +
		`"assignee":"alice","owner":"owner@example.com","due_at":"2025-09-01T00:00:00Z",` +
		`"created_at":"2025-06-01T10:00:00Z","created_by":"olduser","updated_at":"2025-06-02T10:00:00Z",` +
		`"labels":["legacy","auth","legacy"],` +
		`"comments":[{"id":2,"issue_id":"bd-a1.1","author":"bob","text":"Second","created_at":"2025-06-01T12:00:00Z"},` +
		`{"id":1,"issue_id":"bd-a1.1","author":"olduser","text":"First","created_at":"2025-06-01T11:00:00Z"}]}`)
	if res.Imported != 1 || res.Skipped != 0 {
		t.Errorf("result = %+v; want 1 imported, 0 skipped", res)
	}

	got := f.get("bd-a1.1")
	if got.Title != "Old epic" || got.Type != "epic" || got.Priority != 1 || got.Status != "ready" {
		t.Errorf("title, type, priority, status = %q, %q, %d, %q", got.Title, got.Type, got.Priority, got.Status)
	}
	if got.Description != "The description" || got.Design != "The design" ||
		got.AcceptanceCriteria != "The criteria" || got.Notes != "The notes" {
		t.Errorf("texts = %q, %q, %q, %q", got.Description, got.Design, got.AcceptanceCriteria, got.Notes)
	}
	if got.Assignee == nil || *got.Assignee != "alice" || got.Owner == nil || *got.Owner != "owner@example.com" {
		t.Errorf("assignee, owner = %v, %v", got.Assignee, got.Owner)
	}
	if got.DueAt == nil || !got.DueAt.Equal(at("2025-09-01T00:00:00Z")) {
		t.Errorf("due_at = %v", got.DueAt)
	}
	if !got.CreatedAt.Equal(at("2025-06-01T10:00:00Z")) || !got.UpdatedAt.Equal(at("2025-06-02T10:00:00Z")) {
		t.Errorf("created_at, updated_at = %v, %v", got.CreatedAt, got.UpdatedAt)
	}
	if got.CreatedBy != "olduser" || got.StatusReason != nil || got.RetiredAt != nil {
		t.Errorf("created_by, status_reason, retired_at = %q, %v, %v", got.CreatedBy, got.StatusReason, got.RetiredAt)
	}
	wantStrings(t, "labels", got.Labels, "auth", "legacy")

	if len(got.Comments) != 2 {
		t.Fatalf("comments = %+v; want 2", got.Comments)
	}
	first, second := got.Comments[0], got.Comments[1]
	if first.Text != "First" || first.Author != "olduser" || !first.CreatedAt.Equal(at("2025-06-01T11:00:00Z")) {
		t.Errorf("first comment = %+v", first)
	}
	if second.Text != "Second" || second.Author != "bob" {
		t.Errorf("second comment = %+v", second)
	}
	// Like creation, an import is recorded by the issue's own fields.
	f.wantChanges("bd-a1.1")
}

func TestImportMapsOldStatuses(t *testing.T) {
	f := newFixture(t) // the clock reads 2026-01-01
	f.mustImport(
		bd("bd-open", ``),
		strings.Replace(bd("bd-none", ``), `"status":"open",`, ``, 1),
		strings.Replace(bd("bd-wip", ``), `"open"`, `"in_progress"`, 1),
		strings.Replace(bd("bd-hooked", ``), `"open"`, `"hooked"`, 1),
		strings.Replace(bd("bd-blocked", ``), `"open"`, `"blocked"`, 1),
		strings.Replace(bd("bd-pinned", ``), `"open"`, `"pinned"`, 1),
		strings.Replace(bd("bd-later", `,"defer_until":"2026-03-01T00:00:00Z"`), `"open"`, `"deferred"`, 1),
		strings.Replace(bd("bd-someday", ``), `"open"`, `"deferred"`, 1),
		strings.Replace(bd("bd-closed", `,"close_reason":"Fixed in v2","closed_at":"2025-06-07T10:00:00Z"`), `"open"`, `"closed"`, 1),
		strings.Replace(bd("bd-shut", ``), `"open"`, `"closed"`, 1),
	)
	for _, tc := range []struct{ id, status, reason string }{
		{"bd-open", "ready", ""},
		{"bd-none", "ready", ""},
		{"bd-wip", "in-progress", ""},
		{"bd-hooked", "in-progress", ""},
		{"bd-blocked", "held", "imported from bd status blocked"},
		{"bd-pinned", "held", "imported from bd status pinned"},
		{"bd-later", "held", "deferred until 2026-03-01"},
		{"bd-someday", "held", "imported from bd status deferred"},
		{"bd-closed", "done", "Fixed in v2"},
		{"bd-shut", "done", ""},
	} {
		got := f.get(tc.id)
		reason := ""
		if got.StatusReason != nil {
			reason = *got.StatusReason
		}
		if got.Status != tc.status || reason != tc.reason {
			t.Errorf("%s: status %q, reason %q; want %q, %q", tc.id, got.Status, reason, tc.status, tc.reason)
		}
		// A held import's one event says it was ready before (a guess the
		// README owns up to); nothing else has an earlier status.
		if tc.status == "held" {
			if got.PreviousStatus == nil || *got.PreviousStatus != "ready" {
				t.Errorf("%s: previous_status = %v; want ready", tc.id, got.PreviousStatus)
			}
		} else if got.PreviousStatus != nil {
			t.Errorf("%s: previous_status = %q; want none", tc.id, *got.PreviousStatus)
		}
	}
}

// An import gives an issue the one event its old record vouches for: closed
// at closed_at (else updated_at) by closed_by (else the importer); held at
// updated_at by the importer. Open, in-progress, and tombstones have none.
func TestImportRecordsWhenAnIssueWasClosedOrHeld(t *testing.T) {
	f := newFixture(t)
	f.mustImport(
		bd("bd-open", ``),
		strings.Replace(bd("bd-wip", ``), `"open"`, `"in_progress"`, 1),
		strings.Replace(bd("bd-blocked", ``), `"open"`, `"blocked"`, 1),
		strings.Replace(bd("bd-closed", `,"closed_at":"2025-06-07T10:00:00Z","closed_by":"closer"`), `"open"`, `"closed"`, 1),
		strings.Replace(bd("bd-shut", ``), `"open"`, `"closed"`, 1),
		strings.Replace(bd("bd-gone", `,"deleted_at":"2025-06-08T10:00:00Z"`), `"open"`, `"tombstone"`, 1),
	)
	f.wantChanges("bd-open")
	f.wantChanges("bd-wip")
	f.wantChanges("bd-gone")
	f.wantChanges("bd-blocked", change{"status", "ready", "held"})
	f.wantChanges("bd-closed", change{"status", "ready", "done"})
	f.wantChanges("bd-shut", change{"status", "ready", "done"})
	for _, tc := range []struct{ id, at, by string }{
		{"bd-blocked", "2025-06-02T10:00:00Z", "tester"}, // updated_at, the importer
		{"bd-closed", "2025-06-07T10:00:00Z", "closer"},  // closed_at, closed_by
		{"bd-shut", "2025-06-02T10:00:00Z", "tester"},    // no closed_at: updated_at
	} {
		e := f.get(tc.id, tracker.WithHistory()).History[0]
		if !e.ChangedAt.Equal(at(tc.at)) || e.ChangedBy != tc.by {
			t.Errorf("%s: event at %s by %s; want %s by %s", tc.id, e.ChangedAt.Format(time.RFC3339), e.ChangedBy, tc.at, tc.by)
		}
	}
}

func TestImportRetiresTombstones(t *testing.T) {
	f := newFixture(t)
	f.mustImport(
		strings.Replace(bd("bd-gone", `,"deleted_at":"2025-07-01T00:00:00Z","deleted_by":"carol","delete_reason":"duplicate"`), `"open"`, `"tombstone"`, 1),
		strings.Replace(bd("bd-undated", ``), `"open"`, `"tombstone"`, 1),
	)
	gone := f.get("bd-gone")
	if gone.RetiredAt == nil || !gone.RetiredAt.Equal(at("2025-07-01T00:00:00Z")) {
		t.Errorf("retired_at = %v; want deleted_at", gone.RetiredAt)
	}
	if len(gone.Comments) != 1 || gone.Comments[0].Text != "Retired: duplicate" || gone.Comments[0].Author != "carol" {
		t.Errorf("comments = %+v; want the delete reason, as retire keeps it", gone.Comments)
	}
	undated := f.get("bd-undated")
	if undated.RetiredAt == nil || !undated.RetiredAt.Equal(at("2025-06-02T10:00:00Z")) {
		t.Errorf("retired_at = %v; want updated_at when there is no deleted_at", undated.RetiredAt)
	}
	wantStrings(t, "live issues", f.list(tracker.Filter{All: true}))
	wantStrings(t, "retired issues", f.list(tracker.Filter{Retired: true}), "bd-gone", "bd-undated")
}

func TestImportFillsInWhatOldRecordsLeaveOut(t *testing.T) {
	f := newFixture(t)
	f.mustImport(
		`{"id":"bd-bare","title":"Bare","priority":0,"created_at":"2025-06-01T10:00:00Z","updated_at":"2025-06-01T10:00:00Z"}`,
		strings.Replace(bd("bd-gate", ``), `"task"`, `"gate"`, 1),
		"", // a blank line is not a record
	)
	bare := f.get("bd-bare")
	if bare.Type != "task" || bare.Priority != 0 || bare.CreatedBy != "unknown" || bare.Status != "ready" {
		t.Errorf("type, priority, created_by, status = %q, %d, %q, %q", bare.Type, bare.Priority, bare.CreatedBy, bare.Status)
	}
	if got := f.get("bd-gate").Type; got != "task" {
		t.Errorf("a type tess doesn't have imported as %q; want task", got)
	}
}

// dep is a bd dependency of the record it sits in on another issue.
func dep(on, typ string) string { return `{"depends_on_id":"` + on + `","type":"` + typ + `"}` }

func deps(d ...string) string { return `,"dependencies":[` + strings.Join(d, ",") + `]` }

func TestImportMapsOldLinkTypesOntoTheFour(t *testing.T) {
	f := newFixture(t)
	f.mustImport(
		// A record can depend on one further down the file.
		bd("bd-child.1", deps(dep("bd-epic", "parent-child"))),
		bd("bd-epic", ``),
		bd("bd-work", deps(dep("bd-epic", "blocks"), dep("bd-epic", "discovered-from"), dep("bd-child.1", "related"))),
		bd("bd-dup", deps(dep("bd-epic", "duplicates"))),
		bd("bd-new", deps(dep("bd-epic", "supersedes"), dep("bd-work", "waits-for"))),
	)
	child := f.get("bd-child.1")
	if child.Parent == nil || *child.Parent != "bd-epic" {
		t.Errorf("parent of bd-child.1 = %v; want bd-epic", child.Parent)
	}
	wantStrings(t, "bd-child.1 related", child.Links.Related, "bd-work")

	work := f.get("bd-work")
	wantStrings(t, "bd-work blocked_by", work.Links.BlockedBy, "bd-epic")
	wantStrings(t, "bd-work discovered_from", work.Links.DiscoveredFrom, "bd-epic")
	wantStrings(t, "bd-work related", work.Links.Related, "bd-child.1", "bd-new")
	if !work.Blocked {
		t.Error("bd-work is not blocked by its imported blocker")
	}

	epic := f.get("bd-epic")
	wantStrings(t, "bd-epic children", epic.Links.Children, "bd-child.1")
	wantStrings(t, "bd-epic blocks", epic.Links.Blocks, "bd-work")
	wantStrings(t, "bd-epic discovered", epic.Links.Discovered, "bd-work")
	wantStrings(t, "bd-epic related", epic.Links.Related, "bd-dup", "bd-new")

	// Links an issue arrives with are not changes to it.
	for _, id := range []string{"bd-child.1", "bd-work", "bd-dup", "bd-epic"} {
		f.wantChanges(id)
	}
	if got := f.get("bd-work").UpdatedAt; !got.Equal(at("2025-06-02T10:00:00Z")) {
		t.Errorf("updated_at of bd-work = %v; a link it came with moved it", got)
	}
}

func TestImportTakesALinkSaidTwiceOnce(t *testing.T) {
	f := newFixture(t)
	f.mustImport(
		bd("bd-a", deps(dep("bd-b", "related"), dep("bd-b", "duplicates"), dep("bd-b", "blocks"), dep("bd-b", "blocks"))),
		bd("bd-b", deps(dep("bd-a", "related"))),
	)
	a := f.get("bd-a")
	wantStrings(t, "bd-a related", a.Links.Related, "bd-b")
	wantStrings(t, "bd-a blocked_by", a.Links.BlockedBy, "bd-b")
}

func TestReimportSkipsIssuesAlreadyHereAndOverwritesNothing(t *testing.T) {
	f := newFixture(t)
	a, b := bd("bd-a", ``), bd("bd-b", deps(dep("bd-a", "blocks")))
	f.mustImport(a, b)
	f.update("bd-a", tracker.Patch{Title: ptr("Changed locally")})
	f.unlink("bd-a", "blocks", "bd-b")

	res := f.mustImport(a, b, bd("bd-c", deps(dep("bd-a", "related"))))
	if res.Imported != 1 || res.Skipped != 2 {
		t.Errorf("result = %+v; want 1 imported, 2 skipped", res)
	}
	if got := f.get("bd-a").Title; got != "Changed locally" {
		t.Errorf("title of bd-a = %q; the import overwrote it", got)
	}
	// A skipped record's links are skipped with it; a new record can link to
	// an issue that was already here.
	wantStrings(t, "bd-b blocked_by", f.get("bd-b").Links.BlockedBy)
	wantStrings(t, "bd-c related", f.get("bd-c").Links.Related, "bd-a")
}

func TestImportSkipsAnIDTheFileHoldsTwice(t *testing.T) {
	f := newFixture(t)
	res := f.mustImport(bd("bd-a", ``), strings.Replace(bd("bd-a", ``), "Issue bd-a", "Again", 1))
	if res.Imported != 1 || res.Skipped != 1 {
		t.Errorf("result = %+v; want 1 imported, 1 skipped", res)
	}
	if got := f.get("bd-a").Title; got != "Issue bd-a" {
		t.Errorf("title = %q; want the first record's", got)
	}
}

func TestImportIsAllOrNothing(t *testing.T) {
	old := `{"id":"bd-old","title":"Here before","priority":2}`
	for _, tc := range []struct {
		name, code string
		lines      []string
	}{
		{"two issues blocking each other", tracker.CodeCycle, []string{
			bd("bd-ok", ``), bd("bd-x", deps(dep("bd-y", "blocks"))), bd("bd-y", deps(dep("bd-x", "blocks")))}},
		{"a child that blocks its parent's blocker", tracker.CodeCycle, []string{
			bd("bd-ok", ``), bd("bd-p", deps(dep("bd-c", "blocks"))), bd("bd-c", deps(dep("bd-p", "parent-child")))}},
		{"a loop closed through an issue already here", tracker.CodeCycle, []string{
			bd("bd-ok", deps(dep("bd-old", "blocks"))), bd("bd-old", deps(dep("bd-ok", "blocks"))),
			bd("bd-z", deps(dep("bd-ok", "blocks"), dep("bd-z", "blocks")))}},
		{"a parent that blocks its child", tracker.CodeCycle, []string{
			bd("bd-ok", ``), bd("bd-p", ``), bd("bd-c", deps(dep("bd-p", "parent-child"), dep("bd-p", "blocks")))}},
		{"a parent that blocks its child, the blocks link first", tracker.CodeCycle, []string{
			bd("bd-ok", ``), bd("bd-p", ``), bd("bd-c", deps(dep("bd-p", "blocks"), dep("bd-p", "parent-child")))}},
		{"an issue that blocks its grandchild", tracker.CodeCycle, []string{
			bd("bd-ok", ``), bd("bd-m", deps(dep("bd-ok", "parent-child"))),
			bd("bd-g", deps(dep("bd-m", "parent-child"), dep("bd-ok", "blocks")))}},
		{"a parent already here that blocks its new child", tracker.CodeCycle, []string{
			bd("bd-ok", ``), bd("bd-c", deps(dep("bd-old", "parent-child"), dep("bd-old", "blocks")))}},
		{"an issue blocking itself", tracker.CodeCycle, []string{bd("bd-ok", ``), bd("bd-x", deps(dep("bd-x", "blocks")))}},
		{"two parents", tracker.CodeHasParent, []string{
			bd("bd-ok", ``), bd("bd-p", ``), bd("bd-c", deps(dep("bd-ok", "parent-child"), dep("bd-p", "parent-child")))}},
		{"a link to an issue that is nowhere", tracker.CodeInvalidArgument, []string{
			bd("bd-ok", ``), bd("bd-x", deps(dep("bd-missing", "blocks")))}},
		{"a line that is not JSON", tracker.CodeInvalidArgument, []string{bd("bd-ok", ``), `{"id":`}},
		{"no id", tracker.CodeInvalidArgument, []string{bd("bd-ok", ``), `{"title":"No ID","priority":2}`}},
		{"no title", tracker.CodeInvalidArgument, []string{bd("bd-ok", ``), `{"id":"bd-x","priority":2}`}},
		{"a priority out of range", tracker.CodeInvalidArgument, []string{bd("bd-ok", ``), `{"id":"bd-x","title":"X","priority":7}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.mustImport(old)
			_, err := f.importLines(tc.lines...)
			wantCode(t, err, tc.code)
			wantStrings(t, "issues after a refused import", f.list(tracker.Filter{All: true}), "bd-old")
			wantStrings(t, "links of the issue already here", f.get("bd-old").Links.Blocks)
		})
	}
}

// Old bd data often has children blocking their epic (ADR-0016); import
// says why that is refused in the same words link does.
func TestImportRefusesBlocksWithinAHierarchyInHierarchyTerms(t *testing.T) {
	epic := bd("bd-epic", deps(dep("bd-child", "blocks")))
	child := bd("bd-child", deps(dep("bd-epic", "parent-child")))
	for _, lines := range [][]string{{epic, child}, {child, epic}} {
		f := newFixture(t)
		_, err := f.importLines(lines...)
		// Whichever of the two links comes second is the one refused.
		wantRefusalSaying(t, err, tracker.CodeCycle,
			"bd-child", "bd-epic", "a parent's open children already hold it", "dependency loop")
		wantStrings(t, "issues after a refused import", f.list(tracker.Filter{All: true}))
	}

	f := newFixture(t)
	_, err := f.importLines(bd("bd-epic", ``), bd("bd-child", deps(dep("bd-epic", "parent-child"), dep("bd-epic", "blocks"))))
	wantRefusalSaying(t, err, tracker.CodeCycle,
		"bd-epic is an ancestor of bd-child", "so bd-child could never start", "dependency loop")
}

func wantRefusalSaying(t *testing.T, err error, code string, parts ...string) {
	t.Helper()
	wantCode(t, err, code)
	if err == nil {
		return
	}
	for _, part := range parts {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("error %q; want it to say %q", err, part)
		}
	}
}

func TestImportReadsALineOfAnyLength(t *testing.T) {
	f := newFixture(t)
	long := strings.Repeat("x", 200_000) // the old import stopped at 64 KB
	f.mustImport(bd("bd-long", `,"description":"`+long+`"`))
	if got := f.get("bd-long").Description; got != long {
		t.Errorf("description is %d bytes; want %d", len(got), len(long))
	}
}

func TestNewIssuesAfterAnImportUseTheRepoPrefix(t *testing.T) {
	f := newFixture(t)
	f.mustImport(bd("bd-a", ``))
	if id := f.titled("New after import"); !strings.HasPrefix(id, "t-") {
		t.Errorf("new ID %q; want the repo's prefix", id)
	}
}
