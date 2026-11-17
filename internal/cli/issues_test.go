package cli

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// tessIn runs tess with text on stdin.
func tessIn(t *testing.T, stdin string, args ...string) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, strings.NewReader(stdin), &stdout, &stderr)
	return result{code, stdout.String(), stderr.String()}
}

// ok runs tess with --json, fails the test unless it exits 0, and returns
// stdout decoded.
func ok[T any](t *testing.T, args ...string) T {
	t.Helper()
	r := tess(t, append(args, "--json")...)
	if r.code != 0 {
		t.Fatalf("tess %s: exit %d: %s", strings.Join(args, " "), r.code, r.stderr)
	}
	return decode[T](t, r.stdout)
}

type object = map[string]any

func newID(t *testing.T, args ...string) string {
	t.Helper()
	return created(t, args...)["id"].(string)
}

func TestCreateTakesEveryFlagOfTheCommandSurface(t *testing.T) {
	inInitialisedRepo(t)
	parent := newID(t, "Parent")
	blocker := newID(t, "Blocker")
	id := newID(t, "Full", "-t", "bug", "-p", "0", "-d", "Steps", "--design", "A view",
		"--acceptance", "Stays fast", "--notes", "Seen twice", "-l", "ui", "-l", "auth",
		"-a", "alice", "--parent", parent, "-b", blocker)
	got := ok[object](t, "show", id)
	want := object{
		"type": "bug", "priority": 0.0, "description": "Steps", "design": "A view",
		"acceptance_criteria": "Stays fast", "notes": "Seen twice",
		"labels": []any{"auth", "ui"}, "assignee": "alice", "parent": parent,
	}
	for k, v := range want {
		if !reflect.DeepEqual(got[k], v) {
			t.Errorf("%s is %v, want %v", k, got[k], v)
		}
	}
	if by := got["links"].(object)["blocked_by"]; !reflect.DeepEqual(by, []any{blocker}) {
		t.Errorf("blocked_by is %v, want [%s]", by, blocker)
	}
}

func TestATextFlagGivenADashReadsStdinVerbatim(t *testing.T) {
	inInitialisedRepo(t)
	r := tessIn(t, "line one\nline two\n", "create", "From stdin", "-d", "-", "--json")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	if got := decode[object](t, r.stdout)["description"]; got != "line one\nline two\n" {
		t.Errorf("description is %q", got)
	}
	// Stdin can be read once, so only one flag can take it.
	r = tessIn(t, "text", "create", "Twice", "-d", "-", "--notes", "-", "--json")
	wantError(t, r, "invalid_argument")
}

func TestCreateRefusesBadValuesAsInvalidArgument(t *testing.T) {
	inInitialisedRepo(t)
	for _, args := range [][]string{
		{"create", "Bad priority", "-p", "7"},
		{"create", "Bad priority", "-p", "high"},
		{"create", "Bad type", "-t", "saga"},
		{"create"},
	} {
		wantError(t, tess(t, append(args, "--json")...), "invalid_argument")
	}
}

func TestShowWithHistoryAddsTheEvents(t *testing.T) {
	inInitialisedRepo(t)
	id := newID(t, "Tracked")
	if _, has := ok[object](t, "show", id)["history"]; has {
		t.Errorf("show without --history has a history")
	}
	history := ok[object](t, "show", id, "--history")["history"]
	if !reflect.DeepEqual(history, []any{}) {
		t.Errorf("history of a new issue is %v, want []", history)
	}
}

func TestShowWithSeveralIDsIsAnArrayInTheOrderAsked(t *testing.T) {
	inInitialisedRepo(t)
	a, b := newID(t, "First"), newID(t, "Second")
	got := ok[[]object](t, "show", b, a)
	if len(got) != 2 || got[0]["id"] != b || got[1]["id"] != a {
		t.Errorf("show gave %v", got)
	}
	wantError(t, tess(t, "show", a, "t-zzzzzzzz", "--json"), "not_found")
}

func TestPlainShowPrintsTheIssueInFull(t *testing.T) {
	inInitialisedRepo(t)
	blocker := newID(t, "Blocker")
	id := newID(t, "Detailed", "-t", "bug", "-p", "1", "-d", "Steps to reproduce", "-l", "ui", "-a", "alice", "-b", blocker)
	r := tess(t, "show", id)
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	if !strings.HasPrefix(r.stdout, "⊖ "+id+" Detailed\n") {
		t.Errorf("first line is not the list line:\n%s", r.stdout)
	}
	for _, want := range []string{"bug", "waiting", "alice", "ui", "Steps to reproduce", blocker, "tester"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("plain show doesn't mention %q:\n%s", want, r.stdout)
		}
	}
}

func TestUpdateChangesEveryNonStatusField(t *testing.T) {
	inInitialisedRepo(t)
	id := newID(t, "Original", "-l", "keep")
	got := ok[object](t, "update", id, "--title", "New title", "-t", "feature", "-p", "1",
		"-d", "What", "--design", "How", "--acceptance", "When done", "--notes", "Aside",
		"-a", "bob", "--owner", "carol", "--due", "2026-02-01",
		"--add-label", "b", "--add-label", "a", "--remove-label", "keep")
	want := object{
		"title": "New title", "type": "feature", "priority": 1.0, "description": "What",
		"design": "How", "acceptance_criteria": "When done", "notes": "Aside",
		"assignee": "bob", "owner": "carol", "due_at": "2026-02-01T00:00:00Z",
		"labels": []any{"a", "b"},
	}
	for k, v := range want {
		if !reflect.DeepEqual(got[k], v) {
			t.Errorf("%s is %v, want %v", k, got[k], v)
		}
	}
}

func TestUpdateLeavesFieldsThatWereNotNamedAlone(t *testing.T) {
	inInitialisedRepo(t)
	id := newID(t, "Kept", "-p", "0", "-d", "Body", "-a", "alice")
	got := ok[object](t, "update", id, "--owner", "carol")
	if got["priority"] != 0.0 || got["description"] != "Body" || got["assignee"] != "alice" || got["title"] != "Kept" {
		t.Errorf("update --owner changed other fields: %v", got)
	}
}

func TestDueTakesADateOrAnRFC3339TimeAndPrintsUTC(t *testing.T) {
	inInitialisedRepo(t)
	id := newID(t, "Due")
	for given, want := range map[string]string{
		"2026-02-01":                "2026-02-01T00:00:00Z",
		"2026-02-01T12:30:00Z":      "2026-02-01T12:30:00Z",
		"2026-02-01T12:30:00+02:00": "2026-02-01T10:30:00Z",
	} {
		if got := ok[object](t, "update", id, "--due", given)["due_at"]; got != want {
			t.Errorf("--due %s: due_at is %v, want %s", given, got, want)
		}
	}
	for _, bad := range []string{"tomorrow", "2026-02-30", "02/01/2026"} {
		wantError(t, tess(t, "update", id, "--due", bad, "--json"), "invalid_argument")
	}
}

func TestAnEmptyValueClearsAnOptionalField(t *testing.T) {
	inInitialisedRepo(t)
	id := newID(t, "Filled", "-a", "alice", "-d", "Body")
	ok[object](t, "update", id, "--owner", "carol", "--due", "2026-02-01")
	got := ok[object](t, "update", id, "-a", "", "--owner", "", "--due", "", "-d", "")
	if got["assignee"] != nil || got["owner"] != nil || got["due_at"] != nil || got["description"] != "" {
		t.Errorf("not cleared: %v", got)
	}
}

func TestUpdateReadsATextFromStdin(t *testing.T) {
	inInitialisedRepo(t)
	id := newID(t, "Noted")
	r := tessIn(t, "Line\n", "update", id, "--notes", "-", "--json")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	if got := decode[object](t, r.stdout)["notes"]; got != "Line\n" {
		t.Errorf("notes is %q", got)
	}
}

func TestUpdateAppendAddsAParagraph(t *testing.T) {
	inInitialisedRepo(t)
	id := newID(t, "Noted")
	notes := func(r result) any {
		t.Helper()
		if r.code != 0 {
			t.Fatalf("exit %d: %s", r.code, r.stderr)
		}
		return decode[object](t, r.stdout)["notes"]
	}
	if got := notes(tessIn(t, "Line\n", "update", id, "--notes", "-", "--append", "--json")); got != "Line\n" {
		t.Errorf("appending to an empty field gave %q", got)
	}
	if got := notes(tess(t, "update", id, "--notes", "Second", "--append", "--json")); got != "Line\n\nSecond" {
		t.Errorf("appending a paragraph gave %q", got)
	}
	if got := notes(tessIn(t, "Line\n", "update", id, "--append", "--notes", "-", "-p", "1", "--json")); got != "Line\n\nSecond\n\nLine\n" {
		t.Errorf("appending from stdin gave %q", got)
	}
	// Without --append the text is replaced, as before.
	if got := notes(tess(t, "update", id, "--notes", "Only", "--json")); got != "Only" {
		t.Errorf("replacing gave %q", got)
	}
	wantError(t, tess(t, "update", id, "--append", "--json"), "invalid_argument")
	wantError(t, tess(t, "update", id, "--append", "-p", "1", "--json"), "invalid_argument")
	wantError(t, tess(t, "update", id, "--append", "--title", "More", "--json"), "invalid_argument")
	wantError(t, tess(t, "update", id, "--append", "--title", "More", "--notes", "x", "--json"), "invalid_argument")
}

func TestUpdateRefusals(t *testing.T) {
	inInitialisedRepo(t)
	id := newID(t, "Issue")
	wantError(t, tess(t, "update", id, "-p", "9", "--json"), "invalid_argument")
	wantError(t, tess(t, "update", id, "-t", "saga", "--json"), "invalid_argument")
	wantError(t, tess(t, "update", id, "--status", "done", "--json"), "invalid_argument")
	wantError(t, tess(t, "update", id, "--json"), "invalid_argument")
	wantError(t, tess(t, "update", "t-zzzzzzzz", "--title", "x", "--json"), "not_found")
}

func TestPlainUpdatePrintsTheIssueLine(t *testing.T) {
	inInitialisedRepo(t)
	id := newID(t, "Old")
	if r := tess(t, "update", id, "--title", "New"); r.code != 0 || r.stdout != "○ "+id+" New\n" {
		t.Errorf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
}

func ids(issues any) []string {
	out := []string{}
	for _, issue := range issues.([]any) {
		out = append(out, issue.(object)["id"].(string))
	}
	return out
}

func TestStatusPrintsTheIssuesUpdatedAndNewlyUnblocked(t *testing.T) {
	inInitialisedRepo(t)
	blocker := newID(t, "Blocker")
	other := newID(t, "Other")
	dependent := newID(t, "Dependent", "-b", blocker)

	got := ok[object](t, "status", other, "in-progress")
	if !reflect.DeepEqual(ids(got["updated"]), []string{other}) || !reflect.DeepEqual(got["newly_unblocked"], []any{}) {
		t.Errorf("status gave %v", got)
	}
	if status := got["updated"].([]any)[0].(object)["status"]; status != "in-progress" {
		t.Errorf("status is %v", status)
	}

	got = ok[object](t, "status", blocker, other, "done")
	if !reflect.DeepEqual(ids(got["updated"]), []string{blocker, other}) {
		t.Errorf("updated is %v", ids(got["updated"]))
	}
	if !reflect.DeepEqual(ids(got["newly_unblocked"]), []string{dependent}) {
		t.Errorf("newly_unblocked is %v, want [%s]", ids(got["newly_unblocked"]), dependent)
	}
}

func TestStatusTakesAReasonAndForce(t *testing.T) {
	inInitialisedRepo(t)
	blocker := newID(t, "Blocker")
	dependent := newID(t, "Dependent", "-b", blocker)

	wantError(t, tess(t, "status", blocker, "held", "--json"), "reason_required")
	got := ok[object](t, "status", blocker, "held", "-r", "waiting on vendor")
	if reason := got["updated"].([]any)[0].(object)["status_reason"]; reason != "waiting on vendor" {
		t.Errorf("status_reason is %v", reason)
	}

	wantError(t, tess(t, "status", dependent, "done", "--json"), "blocked")
	ok[object](t, "status", dependent, "done", "--force")

	wantError(t, tess(t, "status", blocker, "waiting", "--json"), "invalid_status")
	wantError(t, tess(t, "status", blocker, "--json"), "invalid_argument")
	wantError(t, tess(t, "status", "--json"), "invalid_argument")
}

func TestPlainStatusListsTheIssuesAndWhatTheyUnblocked(t *testing.T) {
	inInitialisedRepo(t)
	blocker := newID(t, "Blocker")
	dependent := newID(t, "Dependent", "-b", blocker)
	r := tess(t, "status", blocker, "done")
	want := "✓ " + blocker + " Blocker\nnewly unblocked:\n  ○ " + dependent + " Dependent\n"
	if r.code != 0 || r.stdout != want {
		t.Errorf("exit %d, stderr %q, stdout:\n%s\nwant:\n%s", r.code, r.stderr, r.stdout, want)
	}
	// Nothing unblocked, nothing said about it.
	if r := tess(t, "status", dependent, "in-progress"); r.stdout != "◐ "+dependent+" Dependent\n" {
		t.Errorf("stdout %q", r.stdout)
	}
}

func TestRetireHidesIssuesAndRestoreBringsOneBack(t *testing.T) {
	inInitialisedRepo(t)
	mistake := newID(t, "Mistake")
	dependent := newID(t, "Dependent", "-b", mistake)

	got := ok[object](t, "retire", mistake, "-r", "created by accident")
	if at := got["updated"].([]any)[0].(object)["retired_at"]; at != "2026-01-01T00:00:00Z" {
		t.Errorf("retired_at is %v", at)
	}
	if !reflect.DeepEqual(ids(got["newly_unblocked"]), []string{dependent}) {
		t.Errorf("newly_unblocked is %v", ids(got["newly_unblocked"]))
	}
	wantError(t, tess(t, "update", mistake, "--title", "x", "--json"), "retired")
	wantError(t, tess(t, "retire", "--json"), "invalid_argument")

	// restore takes the exact ID only.
	wantError(t, tess(t, "restore", mistake[2:], "--json"), "not_found")
	restored := ok[object](t, "restore", mistake)
	if restored["id"] != mistake || restored["retired_at"] != nil {
		t.Errorf("restore gave %v", restored)
	}
	if status := ok[object](t, "show", dependent)["status"]; status != "waiting" {
		t.Errorf("the dependent is %v after the restore, want waiting", status)
	}
}

func TestPlainRetireAndRestoreSayWhatHappened(t *testing.T) {
	inInitialisedRepo(t)
	mistake := newID(t, "Mistake")
	dependent := newID(t, "Dependent", "-b", mistake)
	r := tess(t, "retire", mistake)
	want := "retired " + mistake + " Mistake\nnewly unblocked:\n  ○ " + dependent + " Dependent\n"
	if r.code != 0 || r.stdout != want {
		t.Errorf("retire: exit %d, stderr %q, stdout:\n%s", r.code, r.stderr, r.stdout)
	}
	if r := tess(t, "restore", mistake); r.code != 0 || r.stdout != "restored "+mistake+" Mistake\n" {
		t.Errorf("restore: exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
}

func TestCommentAddsACommentByTheActor(t *testing.T) {
	inInitialisedRepo(t)
	id := newID(t, "Discussed")
	got := ok[object](t, "comment", id, "Looks good")
	if got["author"] != "tester" || got["text"] != "Looks good" || got["created_at"] != "2026-01-01T00:00:00Z" || got["id"] == "" {
		t.Errorf("comment gave %v", got)
	}
	r := tessIn(t, "From a file\nwith two lines\n", "comment", id, "-", "--json")
	if r.code != 0 || decode[object](t, r.stdout)["text"] != "From a file\nwith two lines\n" {
		t.Errorf("comment -: exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	comments := ok[object](t, "show", id)["comments"].([]any)
	if len(comments) != 2 || comments[0].(object)["text"] != "Looks good" {
		t.Errorf("show has comments %v", comments)
	}
	if r := tess(t, "comment", id, "Plain"); r.code != 0 || r.stdout != "commented on "+id+"\n" {
		t.Errorf("plain comment: exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	wantError(t, tess(t, "comment", id, "--json"), "invalid_argument")
	wantError(t, tess(t, "comment", "t-zzzzzzzz", "x", "--json"), "not_found")
}

// ADR-0013: `show --history` shows the log interleaved with the comments.
func TestPlainShowWithHistoryInterleavesCommentsWithTheEvents(t *testing.T) {
	inInitialisedRepo(t)
	at(t, 1)
	id := newID(t, "Tracked")
	at(t, 2)
	ok[object](t, "status", id, "in-progress")
	at(t, 3)
	ok[object](t, "comment", id, "Halfway there\nsecond line")
	at(t, 4)
	ok[object](t, "update", id, "-p", "0")

	r := tess(t, "show", id, "--history")
	_, history, found := strings.Cut(r.stdout, "\nHistory:\n")
	want := "  2026-01-01T00:00:12Z tester: status ready → in-progress\n" +
		"  2026-01-01T00:00:13Z tester commented:\n" +
		"    Halfway there\n" +
		"    second line\n" +
		"  2026-01-01T00:00:14Z tester: priority 2 → 0\n"
	if r.code != 0 || !found || history != want {
		t.Errorf("exit %d, stderr %q, history:\n%s\nwant:\n%s", r.code, r.stderr, history, want)
	}
	if strings.Contains(r.stdout, "Comments:") {
		t.Errorf("the comments are listed apart from the history:\n%s", r.stdout)
	}
	// Without --history the comments have their own section.
	if r := tess(t, "show", id); !strings.Contains(r.stdout, "\nComments:\n  2026-01-01T00:00:13Z tester:\n    Halfway there\n") {
		t.Errorf("plain show without --history:\n%s", r.stdout)
	}
}

// ADR-0018: when an issue shows as waiting, the output of status says why.
func TestPlainStatusSaysWhyAnIssueShowsAsWaiting(t *testing.T) {
	inInitialisedRepo(t)
	at(t, 1)
	gate := newID(t, "Gate")
	at(t, 2)
	other := newID(t, "Other")
	at(t, 3)
	parent := newID(t, "Parent", "-b", gate, "-b", other)
	at(t, 4)
	child := newID(t, "Child", "--parent", parent)

	wantPlain(t, tess(t, "status", parent, "ready"),
		"⊖ "+parent+" Parent",
		"  shows as waiting: blocked by "+gate+", "+other,
	)
	wantPlain(t, tess(t, "status", child, "ready"),
		"⊖ "+child+" Child",
		"  shows as waiting: blocked through "+parent,
	)
	ok[object](t, "status", gate, other, "done")
	wantPlain(t, tess(t, "status", parent, "ready"),
		"⊖ "+parent+" Parent",
		"  shows as waiting: it has open children",
	)
	// A status that is not waiting says nothing more, blocked or not.
	wantPlain(t, tess(t, "status", child, "ready"), "○ "+child+" Child")
	ok[object](t, "status", gate, "ready")
	wantPlain(t, tess(t, "status", parent, "in-progress"), "◐ "+parent+" Parent")
}

func TestShowWithTreeAddsTheNestedNodesBelowTheIssue(t *testing.T) {
	inInitialisedRepo(t)
	root, child, blocker := newID(t, "Root"), newID(t, "Child"), newID(t, "Blocker")
	ok[[]any](t, "link", root, "parent-of", child)
	ok[[]any](t, "link", blocker, "blocks", child)

	if _, has := ok[object](t, "show", root)["tree"]; has {
		t.Errorf("show without --tree has a tree")
	}
	got := ok[object](t, "show", root, "--tree")
	want := object{
		"id": root, "title": "Root", "status": "waiting", "blocked_by": []any{},
		"children": []any{object{
			"id": child, "title": "Child", "status": "waiting", "children": []any{},
			"blocked_by": []any{object{
				"id": blocker, "title": "Blocker", "status": "ready",
				"children": []any{}, "blocked_by": []any{},
			}},
		}},
	}
	if !reflect.DeepEqual(got["tree"], want) {
		t.Errorf("tree is %v, want %v", got["tree"], want)
	}
	// The tree is one more key of the issue object, not a different result.
	if got["id"] != root || got["links"] == nil || got["comments"] == nil {
		t.Errorf("show --tree is not the issue object: %v", got)
	}
	// Each of several issues carries its own.
	both := ok[[]object](t, "show", child, blocker, "--tree")
	if len(both) != 2 || both[0]["tree"].(object)["id"] != child || both[1]["tree"].(object)["id"] != blocker {
		t.Errorf("show of two issues with --tree gave %v", both)
	}
}

func TestPlainShowWithTreePrintsTheTreeIndentedUnderTheIssue(t *testing.T) {
	inInitialisedRepo(t)
	root, child, blocker := newID(t, "Root"), newID(t, "Child"), newID(t, "Blocker")
	tess(t, "link", root, "parent-of", child)
	tess(t, "link", blocker, "blocks", child)

	plain := tess(t, "show", root)
	r := tess(t, "show", root, "--tree")
	want := plain.stdout + strings.Join([]string{
		"",
		"Tree:",
		"  ⊖ " + child + " Child",
		"    blocked by ○ " + blocker + " Blocker",
		"",
	}, "\n")
	if r.code != 0 || r.stdout != want {
		t.Errorf("exit %d (%s), stdout:\n%s\nwant:\n%s", r.code, r.stderr, r.stdout, want)
	}

	// An issue with nothing under it says so.
	r = tess(t, "show", blocker, "--tree")
	if !strings.HasSuffix(r.stdout, "\nTree:\n  no children or blockers\n") {
		t.Errorf("show --tree of a leaf:\n%s", r.stdout)
	}
}

func TestThereIsNoTreeCommand(t *testing.T) {
	inInitialisedRepo(t)
	id := newID(t, "A")
	wantError(t, tess(t, "tree", id, "--json"), "invalid_argument")
	for _, args := range [][]string{{}, {"--help"}, {"show", "--help"}} {
		if r := tess(t, args...); strings.Contains(r.stdout, "tess tree") || strings.Contains(r.stdout, "\n  tree ") {
			t.Errorf("tess %v still names the tree command:\n%s", args, r.stdout)
		}
	}
	if strings.Contains(string(skillText), "tess tree") {
		t.Errorf("the skill still names `tess tree`")
	}
	if !strings.Contains(string(skillText), "--tree") {
		t.Errorf("the skill doesn't mention `tess show ID --tree`")
	}
}

func TestShowFieldPrintsOneFieldRaw(t *testing.T) {
	inInitialisedRepo(t)
	blocker := newID(t, "Blocker")
	id := newID(t, "Full", "-t", "bug", "-p", "0", "--notes", "Seen twice\n\nand again", "-d", "ends in a newline\n",
		"-l", "ui", "-l", "auth", "-b", blocker)
	for _, tc := range []struct {
		field, plain, json string
	}{
		// Text fields exactly as stored: no newline added, none taken away.
		{"notes", "Seen twice\n\nand again", `"Seen twice\n\nand again"` + "\n"},
		{"description", "ends in a newline\n", `"ends in a newline\n"` + "\n"},
		{"design", "", `""` + "\n"},
		// Scalars on one line.
		{"title", "Full\n", `"Full"` + "\n"},
		{"priority", "0\n", "0\n"},
		{"blocked", "true\n", "true\n"},
		{"status", "waiting\n", `"waiting"` + "\n"},
		{"created_at", "2026-01-01T00:00:00Z\n", `"2026-01-01T00:00:00Z"` + "\n"},
		// null as nothing at all.
		{"assignee", "", "null\n"},
		{"previous_status", "", "null\n"},
		// A list of plain values, one to a line.
		{"labels", "auth\nui\n", `["auth","ui"]` + "\n"},
		{"ancestors", "", "[]\n"},
	} {
		if r := tess(t, "show", id, "--field", tc.field); r.code != 0 || r.stdout != tc.plain {
			t.Errorf("--field %s: exit %d (%s), stdout %q, want %q", tc.field, r.code, r.stderr, r.stdout, tc.plain)
		}
		if r := tess(t, "show", id, "--field", tc.field, "--json"); r.code != 0 || r.stdout != tc.json {
			t.Errorf("--field %s --json: exit %d (%s), stdout %q, want %q", tc.field, r.code, r.stderr, r.stdout, tc.json)
		}
	}
}

func TestShowFieldReachesEveryKeyOfTheIssueObject(t *testing.T) {
	inInitialisedRepo(t)
	blocker := newID(t, "Blocker")
	id := newID(t, "Full", "-b", blocker)
	tess(t, "update", id, "--title", "Fuller")

	links := ok[object](t, "show", id, "--field", "links")
	if !reflect.DeepEqual(links["blocked_by"], []any{blocker}) {
		t.Errorf("--field links is %v", links)
	}
	// history and tree are keys too, and asking for one loads it.
	history := ok[[]any](t, "show", id, "--field", "history")
	if len(history) != 1 || history[0].(object)["attr"] != "title" {
		t.Errorf("--field history is %v", history)
	}
	tree := ok[object](t, "show", id, "--field", "tree")
	if tree["id"] != id || len(tree["blocked_by"].([]any)) != 1 {
		t.Errorf("--field tree is %v", tree)
	}
	// In plain mode a value with structure is printed as JSON.
	r := tess(t, "show", id, "--field", "links")
	if got := decode[object](t, r.stdout); !reflect.DeepEqual(got, links) {
		t.Errorf("plain --field links is %q", r.stdout)
	}
}

func TestShowFieldRefusals(t *testing.T) {
	inInitialisedRepo(t)
	a, b := newID(t, "A"), newID(t, "B")
	unknown := wantError(t, tess(t, "show", a, "--field", "nope", "--json"), "invalid_argument")
	if msg, _ := unknown["message"].(string); !strings.Contains(msg, "acceptance_criteria") {
		t.Errorf("the message doesn't list the fields: %v", unknown["message"])
	}
	wantError(t, tess(t, "show", a, b, "--field", "notes", "--json"), "invalid_argument")
	wantError(t, tess(t, "show", a, "--field", "", "--json"), "invalid_argument")
	wantError(t, tess(t, "show", "t-zzzzzzzz", "--field", "notes", "--json"), "not_found")
}
