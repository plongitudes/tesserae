package cli

import (
	"strings"
	"testing"
)

// quiet checks that a hook ran without a word and without failing.
func quiet(t *testing.T, what string, r result) {
	t.Helper()
	if r.code != 0 || r.stdout != "" || r.stderr != "" {
		t.Errorf("%s: exit %d, stdout %q, stderr %q; want exit 0 and nothing printed", what, r.code, r.stdout, r.stderr)
	}
}

func TestHookSessionStartPrintsFactsAboutTheIssues(t *testing.T) {
	inInitialisedRepo(t)
	blocker := newID(t, "Blocker")
	newID(t, "Blocked", "--blocked-by", blocker)
	newID(t, "Free")
	started := newID(t, "Started")
	finished := newID(t, "Finished")
	ok[object](t, "status", started, "in-progress")
	ok[object](t, "status", finished, "done")

	want := []string{
		"This repository tracks its issues with tess.",
		"Ready to start: 2",
		"In progress: 1",
		"Waiting on other issues: 1",
		"Last sync: never",
	}
	for _, stdin := range []string{
		`{"session_id":"abc","hook_event_name":"SessionStart","source":"startup"}`,
		`{"session_id":"abc","hook_event_name":"SessionStart","source":"resume"}`,
		`{"session_id":"abc","hook_event_name":"SessionStart","source":"compact"}`,
		``,
		`not json`,
	} {
		wantPlain(t, tessIn(t, stdin, "hook", "session-start"), want...)
	}
}

func TestHookSessionStartCountsEveryReadyIssueNotOnlyTheFirstTen(t *testing.T) {
	inInitialisedRepo(t)
	for range 12 {
		newID(t, "One of many")
	}
	r := tessIn(t, `{"source":"startup"}`, "hook", "session-start")
	if !strings.Contains(r.stdout, "Ready to start: 12\n") {
		t.Errorf("stdout is %q", r.stdout)
	}
}

func TestHookSessionStartIsSilentWhereThereIsNothingToReport(t *testing.T) {
	start := func() result { return tessIn(t, `{"source":"startup"}`, "hook", "session-start") }

	inDir(t, isolate(t), "nowhere")
	quiet(t, "outside a repository", start())

	inRepo(t, "repo")
	quiet(t, "with no database", start())

	tess(t, "init", "--prefix", "t")
	t.Setenv("TESS_NOW", "yesterday")
	quiet(t, "with a clock it can't read", start())
}

func TestHookSessionEndDoesNothing(t *testing.T) {
	inDir(t, isolate(t), "nowhere")
	quiet(t, "outside a repository", tessIn(t, `{"reason":"other"}`, "hook", "session-end"))
	inInitialisedRepo(t)
	quiet(t, "in a repository", tessIn(t, `{"reason":"other"}`, "hook", "session-end"))
}

func TestHookTakesOnlyItsTwoEvents(t *testing.T) {
	inInitialisedRepo(t)
	wantError(t, tess(t, "hook", "--json"), "invalid_argument")
	wantError(t, tess(t, "hook", "stop", "--json"), "invalid_argument")
	wantError(t, tess(t, "hook", "session-start", "extra", "--json"), "invalid_argument")
}

func TestOrientationDoesNotShowTheHookPlumbing(t *testing.T) {
	inDir(t, isolate(t), "anywhere")
	if r := tess(t); strings.Contains(r.stdout, "hook") {
		t.Errorf("orientation mentions hook:\n%s", r.stdout)
	}
}
