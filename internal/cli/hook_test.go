package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// detached is every process a test's hooks asked to leave running. The
// tests never start one: the program would be the test binary itself.
var detached [][]string

func init() {
	detach = func(program string, args ...string) error {
		detached = append(detached, args)
		return nil
	}
}

// start runs the session-start hook as Claude Code does at a startup.
func start(t *testing.T) result {
	t.Helper()
	return tessIn(t, `{"source":"startup"}`, "hook", "session-start")
}

// wantLastSync checks the hook's last line, and that it said nothing on
// stderr and did not fail.
func wantLastSync(t *testing.T, r result, fact string) {
	t.Helper()
	if r.code != 0 || r.stderr != "" {
		t.Errorf("exit %d, stderr %q; want exit 0 and nothing on stderr", r.code, r.stderr)
	}
	if want := "Last sync: " + fact + "\n"; !strings.HasSuffix(r.stdout, "\n"+want) {
		t.Errorf("stdout is\n%swant it to end with %q", r.stdout, want)
	}
}

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

func TestHookSessionStartSyncsFirstAndSaysJustNow(t *testing.T) {
	root := copies(t, "a", "b")
	inCopy(t, root, "b", "2026-01-01T00:00:00Z")
	created(t, "From b")
	must(t, "sync")
	inCopy(t, root, "a", "2026-01-01T00:00:00Z")
	created(t, "From a")

	r := start(t)
	wantLastSync(t, r, "just now")
	if !strings.Contains(r.stdout, "Ready to start: 2\n") {
		t.Errorf("the facts were counted before the sync:\n%s", r.stdout)
	}
	if r := must(t, "sync"); r.stdout != "up to date\n" {
		t.Errorf("after the hook, sync says %q, want up to date", r.stdout)
	}
}

func TestHookSessionStartAfterACompactionDoesNotSync(t *testing.T) {
	root := copies(t, "a")
	inCopy(t, root, "a", "2026-01-01T00:00:00Z")
	created(t, "One")
	compact := func() result { return tessIn(t, `{"source":"compact"}`, "hook", "session-start") }
	wantLastSync(t, compact(), "never")

	must(t, "sync")
	t.Setenv("TESS_NOW", "2026-01-01T00:00:59Z")
	wantLastSync(t, compact(), "just now")
	t.Setenv("TESS_NOW", "2026-01-01T02:10:00Z")
	wantLastSync(t, compact(), "2 hours ago")
	created(t, "Two")
	wantLastSync(t, compact(), "2 hours ago, 1 issue changed since")
	created(t, "Three")
	wantLastSync(t, compact(), "2 hours ago, 2 issues changed since")
	if r := must(t, "sync"); r.stdout != "fetched 0 issues, pushed 2\n" {
		t.Errorf("after the hook, sync says %q: the hook synced", r.stdout)
	}
}

func TestHookSessionStartSaysWhyACopyThatNeverSyncedDidNot(t *testing.T) {
	root := copies(t, "a")
	inCopy(t, root, "a", "2026-01-01T00:00:00Z")
	gitIn(t, ".", "remote", "set-url", "origin", filepath.Join(root, "gone.git"))
	wantLastSync(t, start(t), "not synced (offline)")
}

func TestHookSessionStartKeepsTheTimeOfTheLastSyncWhenThisOneFails(t *testing.T) {
	root := copies(t, "a")
	inCopy(t, root, "a", "2026-01-01T00:00:00Z")
	created(t, "One")
	must(t, "sync")

	t.Setenv("TESS_NOW", "2026-01-04T00:00:00Z")
	gitIn(t, ".", "remote", "set-url", "origin", filepath.Join(root, "gone.git"))
	wantLastSync(t, start(t), "3 days ago, not synced now (offline)")
	gitIn(t, ".", "remote", "remove", "origin")
	wantLastSync(t, start(t), "3 days ago, not synced now (no_remote)")
}

func TestHookSessionStartGivesTheSyncADeadline(t *testing.T) {
	root := copies(t, "a")
	inCopy(t, root, "a", "2026-01-01T00:00:00Z")
	created(t, "One")
	deadline := syncDeadline
	t.Cleanup(func() { syncDeadline = deadline })
	if syncDeadline != 10*time.Second {
		t.Errorf("the deadline is %v, want ten seconds", syncDeadline)
	}
	syncDeadline = time.Nanosecond
	r := start(t)
	wantLastSync(t, r, "not synced (timed out; run tess sync to finish any login)")
	if !strings.Contains(r.stdout, "Ready to start: 1\n") {
		t.Errorf("stdout is\n%s", r.stdout)
	}
}

func TestHookSessionStartNeverPrintsAResolution(t *testing.T) {
	_, id := titleConflict(t)
	wantLastSync(t, start(t), "just now")
	if title := ok[object](t, "show", id)["title"]; title != "From b" {
		t.Errorf("title is %v: the hook's sync did not merge", title)
	}
}

func TestSpellsOutHowLongAgo(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0:                "just now",
		59 * time.Second: "just now",
		-time.Hour:       "just now", // a clock set back
		time.Minute:      "1 minute ago",
		59 * time.Minute: "59 minutes ago",
		time.Hour:        "1 hour ago",
		47 * time.Hour:   "47 hours ago",
		48 * time.Hour:   "2 days ago",
		400 * time.Hour:  "16 days ago",
	} {
		if got := ago(d); got != want {
			t.Errorf("ago(%v) is %q, want %q", d, got, want)
		}
	}
}

func TestHookSessionEndLeavesASyncRunningAndSaysNothing(t *testing.T) {
	end := func() result { return tessIn(t, `{"reason":"other"}`, "hook", "session-end") }
	detached = nil

	inDir(t, isolate(t), "nowhere")
	quiet(t, "outside a repository", end())
	inRepo(t, "repo")
	quiet(t, "with no database", end())
	if len(detached) != 0 {
		t.Errorf("started %v where there is nothing to sync", detached)
	}

	tess(t, "init", "--prefix", "t")
	quiet(t, "in a repository", end())
	if len(detached) != 1 || strings.Join(detached[0], " ") != "sync" {
		t.Errorf("started %v, want one `sync`", detached)
	}
}

func TestHookSessionEndIsQuietWhenNothingCanBeStarted(t *testing.T) {
	inInitialisedRepo(t)
	was := detach
	t.Cleanup(func() { detach = was })
	detach = func(string, ...string) error { return os.ErrPermission }
	quiet(t, "when the sync can't be started", tessIn(t, ``, "hook", "session-end"))
}

func TestStartDetachedDoesNotWaitForTheProcess(t *testing.T) {
	dir := t.TempDir()
	done := filepath.Join(dir, "done")
	began := time.Now()
	// The process outlives the call, and its output goes nowhere.
	if err := startDetached("/bin/sh", "-c", "sleep 1; echo noise; echo noise >&2; echo ok > "+done); err != nil {
		t.Fatal(err)
	}
	if waited := time.Since(began); waited > 900*time.Millisecond {
		t.Errorf("startDetached took %v: it waited for the process", waited)
	}
	if _, err := os.Stat(done); err == nil {
		t.Fatal("the process had finished before startDetached returned")
	}
	for range 100 {
		if _, err := os.Stat(done); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("the detached process never finished")
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
