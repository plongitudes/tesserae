package cli

import (
	"bytes"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// onTTY runs tess as it runs with stdout on a terminal.
func onTTY(t *testing.T, args ...string) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	a := &app{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr, tty: true}
	code := a.run(args)
	return result{code, stdout.String(), stderr.String()}
}

// at moves the clock, so issues created one after another sort that way.
func at(t *testing.T, second int) {
	t.Helper()
	t.Setenv("TESS_NOW", "2026-01-01T00:00:"+strconv.Itoa(10+second)+"Z")
}

// listed runs a list command with --json and returns the IDs in order.
func listed(t *testing.T, args ...string) []string {
	t.Helper()
	ids := []string{}
	for _, issue := range ok[[]object](t, args...) {
		ids = append(ids, issue["id"].(string))
	}
	return ids
}

func wantIDs(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %v, want %v", what, got, want)
	}
}

func wantPlain(t *testing.T, r result, lines ...string) {
	t.Helper()
	want := strings.Join(lines, "\n") + "\n"
	if len(lines) == 0 {
		want = ""
	}
	if r.code != 0 || r.stdout != want {
		t.Errorf("exit %d, stderr %q, stdout:\n%s\nwant:\n%s", r.code, r.stderr, r.stdout, want)
	}
}

// project is an epic with two children, a child of the first child, and a
// top-level chore; `low` sorts last and `chore` between the epic's children.
type project struct{ epic, first, second, grand, chore string }

func newProject(t *testing.T) project {
	t.Helper()
	inInitialisedRepo(t)
	var p project
	at(t, 1)
	p.epic = newID(t, "Epic", "-t", "epic", "-p", "1")
	at(t, 2)
	p.first = newID(t, "First", "--parent", p.epic, "-p", "3", "-l", "auth", "-a", "alice")
	at(t, 3)
	p.second = newID(t, "Second", "--parent", p.epic, "-p", "1", "-l", "auth")
	at(t, 4)
	p.grand = newID(t, "Grand", "--parent", p.first, "-p", "0", "-d", "Mentions Tokens")
	at(t, 5)
	p.chore = newID(t, "Chore", "-t", "chore", "-p", "2")
	return p
}

func TestReadyListsOnlyReadyIssuesInListOrder(t *testing.T) {
	p := newProject(t)
	// The epic and First have open children, so they are waiting.
	wantIDs(t, "ready", listed(t, "ready"), p.grand, p.second, p.chore)
	ok[object](t, "status", p.second, "in-progress")
	wantIDs(t, "ready after a start", listed(t, "ready"), p.grand, p.chore)
}

func TestReadyShowsTenUnlessToldOtherwise(t *testing.T) {
	inInitialisedRepo(t)
	for i := range 12 {
		at(t, i)
		newID(t, "Issue "+strconv.Itoa(i))
	}
	if got := len(listed(t, "ready")); got != 10 {
		t.Errorf("ready listed %d, want 10", got)
	}
	if got := len(listed(t, "ready", "-n", "3")); got != 3 {
		t.Errorf("ready -n 3 listed %d", got)
	}
	if got := len(listed(t, "ready", "-n", "0")); got != 12 {
		t.Errorf("ready -n 0 listed %d, want all 12", got)
	}
	if got := len(listed(t, "list")); got != 12 {
		t.Errorf("list listed %d, want all 12", got)
	}
	if got := len(listed(t, "list", "-n", "5")); got != 5 {
		t.Errorf("list -n 5 listed %d", got)
	}
	wantError(t, tess(t, "list", "-n", "-1", "--json"), "invalid_argument")
	wantError(t, tess(t, "ready", "-n", "many", "--json"), "invalid_argument")
}

func TestListTakesEveryFilterOfTheCommandSurface(t *testing.T) {
	p := newProject(t)
	at(t, 6)
	finished := newID(t, "Finished", "-p", "4")
	ok[object](t, "status", finished, "done")
	at(t, 7)
	gone := newID(t, "Gone", "-p", "4")
	ok[object](t, "retire", gone)
	ok[[]any](t, "link", p.chore, "blocks", p.second)

	all := []string{p.grand, p.epic, p.second, p.chore, p.first}
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{nil, all},
		{[]string{"--all"}, append(append([]string{}, all...), finished)},
		{[]string{"--retired"}, []string{gone}},
		{[]string{"--status", "waiting"}, []string{p.epic, p.second, p.first}},
		{[]string{"--status", "done"}, []string{finished}},
		{[]string{"--type", "chore"}, []string{p.chore}},
		{[]string{"--label", "auth"}, []string{p.second, p.first}},
		{[]string{"--assignee", "alice"}, []string{p.first}},
		{[]string{"--parent", p.epic}, []string{p.grand, p.second, p.first}},
		{[]string{"--blocked"}, []string{p.second}},
		{[]string{"-s", "tokens"}, []string{p.grand}},
		{[]string{"--search", "chor"}, []string{p.chore}},
		{[]string{"--label", "auth", "--assignee", "alice"}, []string{p.first}},
	} {
		wantIDs(t, "list "+strings.Join(tc.args, " "), listed(t, append([]string{"list"}, tc.args...)...), tc.want...)
	}
	wantError(t, tess(t, "list", "--status", "nonsense", "--json"), "invalid_status")
	wantError(t, tess(t, "list", "--parent", "t-nope", "--json"), "not_found")
	wantError(t, tess(t, "list", "stray", "--json"), "invalid_argument")
}

func TestListJSONIsFlatAndCarriesTheHierarchyEvenWithTree(t *testing.T) {
	p := newProject(t)
	for _, args := range [][]string{{"list"}, {"list", "--tree"}, {"list", "--flat"}} {
		issues := ok[[]object](t, args...)
		wantIDs(t, strings.Join(args, " "), listed(t, args...), p.grand, p.epic, p.second, p.chore, p.first)
		if got := issues[0]["ancestors"]; !reflect.DeepEqual(got, []any{p.first, p.epic}) {
			t.Errorf("%v: ancestors of the grandchild are %v", args, got)
		}
		if _, has := issues[0]["links"]; has {
			t.Errorf("%v: a listed issue carries links", args)
		}
	}
}

func TestPlainListsAreFlatOffATerminal(t *testing.T) {
	p := newProject(t)
	wantPlain(t, tess(t, "ready"),
		"○ "+p.grand+" Grand",
		"○ "+p.second+" Second",
		"○ "+p.chore+" Chore",
	)
	ok[object](t, "status", p.second, "in-progress")
	ok[object](t, "status", p.chore, "held", "-r", "later")
	wantPlain(t, tess(t, "list"),
		"○ "+p.grand+" Grand",
		"⊖ "+p.epic+" Epic",
		"◐ "+p.second+" Second",
		"❄ "+p.chore+" Chore",
		"⊖ "+p.first+" First",
	)
}

func TestAnEmptyPlainListPrintsNothing(t *testing.T) {
	inInitialisedRepo(t)
	wantPlain(t, tess(t, "ready"))
	wantPlain(t, tess(t, "list", "--tree"))
}

func TestTreeShowsAncestorsAsHeadersAndIndentsResults(t *testing.T) {
	p := newProject(t)
	// The epic and First are not ready: they are context for the results.
	// Groups sort by their best result, so the epic's group (Grand, p0)
	// comes before Chore, and inside it First's group before Second.
	wantPlain(t, tess(t, "ready", "--tree"),
		"⊖ "+p.epic+" Epic",
		"  ⊖ "+p.first+" First",
		"    ○ "+p.grand+" Grand",
		"  ○ "+p.second+" Second",
		"○ "+p.chore+" Chore",
	)
}

func TestTreePutsAResultThatIsAlsoAParentInItsPlaceOnce(t *testing.T) {
	p := newProject(t)
	wantPlain(t, tess(t, "list", "--tree"),
		"⊖ "+p.epic+" Epic",
		"  ⊖ "+p.first+" First",
		"    ○ "+p.grand+" Grand",
		"  ○ "+p.second+" Second",
		"○ "+p.chore+" Chore",
	)
}

func TestTheLimitCountsResultsNotHeaders(t *testing.T) {
	p := newProject(t)
	wantPlain(t, tess(t, "ready", "--tree", "-n", "2"),
		"⊖ "+p.epic+" Epic",
		"  ⊖ "+p.first+" First",
		"    ○ "+p.grand+" Grand",
		"  ○ "+p.second+" Second",
	)
}

func TestAHeaderCanBeAnIssueTheFilterLeavesOut(t *testing.T) {
	inInitialisedRepo(t)
	at(t, 1)
	parent := newID(t, "Parent")
	at(t, 2)
	child := newID(t, "Child", "--parent", parent)
	ok[object](t, "status", child, "done")
	ok[object](t, "status", parent, "done")
	wantPlain(t, tess(t, "list", "--status", "done", "--type", "task", "-s", "chil", "--tree"),
		"✓ "+parent+" Parent",
		"  ✓ "+child+" Child",
	)
}

const (
	dim   = "\x1b[2m"
	reset = "\x1b[0m"
)

func TestOnATerminalListsAreTreesWithDimmedHeaders(t *testing.T) {
	p := newProject(t)
	t.Setenv("NO_COLOR", "")
	r := onTTY(t, "ready", "-n", "1")
	wantPlain(t, r,
		dim+"⊖ "+p.epic+" Epic"+reset,
		dim+"  ⊖ "+p.first+" First"+reset,
		"    ○ "+p.grand+" Grand",
	)
}

func TestFlatForcesOneLinePerResultOnATerminal(t *testing.T) {
	p := newProject(t)
	t.Setenv("NO_COLOR", "")
	wantPlain(t, onTTY(t, "ready", "--flat"),
		"○ "+p.grand+" Grand",
		"○ "+p.second+" Second",
		"○ "+p.chore+" Chore",
	)
}

func TestColourIsOnlyForATerminalAndFollowsTheStatus(t *testing.T) {
	p := newProject(t)
	ok[object](t, "status", p.chore, "done")
	t.Setenv("NO_COLOR", "")
	want := "\x1b[32m✓\x1b[0m " + p.chore + " Chore\n"
	if r := onTTY(t, "list", "--status", "done"); r.stdout != want {
		t.Errorf("on a terminal: %q, want %q", r.stdout, want)
	}
	for _, args := range [][]string{{"list", "--all"}, {"list", "--all", "--tree"}} {
		if r := tess(t, args...); strings.Contains(r.stdout, "\x1b") {
			t.Errorf("%v off a terminal has escape codes: %q", args, r.stdout)
		}
	}
	t.Setenv("NO_COLOR", "1")
	if r := onTTY(t, "list", "--all"); strings.Contains(r.stdout, "\x1b") {
		t.Errorf("NO_COLOR on a terminal has escape codes: %q", r.stdout)
	}
	if r := onTTY(t, "list", "--all", "--json"); strings.Contains(r.stdout, "\x1b") {
		t.Errorf("--json on a terminal has escape codes: %q", r.stdout)
	}
}

func TestFlatAndTreeTogetherAreRefused(t *testing.T) {
	inInitialisedRepo(t)
	wantError(t, tess(t, "list", "--flat", "--tree", "--json"), "invalid_argument")
	wantError(t, tess(t, "ready", "--flat", "--tree", "--json"), "invalid_argument")
}

func TestBlockedListsShowWhatBlocksEachIssue(t *testing.T) {
	inInitialisedRepo(t)
	at(t, 1)
	gate := newID(t, "Gate")
	at(t, 2)
	other := newID(t, "Other")
	at(t, 3)
	parent := newID(t, "Parent")
	at(t, 4)
	child := newID(t, "Child", "--parent", parent)
	ok[[]any](t, "link", gate, "blocks", parent)
	ok[[]any](t, "link", other, "blocks", parent)
	ok[object](t, "status", other, "done")
	// Other is done, so only Gate is named; Child is blocked by way of
	// its parent.
	wantPlain(t, tess(t, "list", "--blocked"),
		"⊖ "+parent+" Parent (blocked by "+gate+")",
		"⊖ "+child+" Child (blocked through "+parent+")",
	)
}

func TestAParentListedAfterItsChildStillSortsByTheChild(t *testing.T) {
	inInitialisedRepo(t)
	at(t, 1)
	alone := newID(t, "Alone", "-p", "1")
	at(t, 2)
	parent := newID(t, "Parent", "-p", "4")
	at(t, 3)
	late := newID(t, "Late child", "--parent", parent, "-p", "3")
	at(t, 4)
	early := newID(t, "Early child", "--parent", parent, "-p", "0")
	// The parent is itself a result, last in list order, but its group's
	// best result is the p0 child, so the group leads.
	wantPlain(t, tess(t, "list", "--tree"),
		"⊖ "+parent+" Parent",
		"  ○ "+early+" Early child",
		"  ○ "+late+" Late child",
		"○ "+alone+" Alone",
	)
}
