package cli

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// copies makes a bare remote and, for each name, a git repository with it
// as origin and an initialised tracker with the prefix t: two copies of one
// tracker when two names are given. It returns the root holding them all.
func copies(t *testing.T, names ...string) string {
	t.Helper()
	root := isolate(t)
	gitIn(t, root, "init", "-q", "--bare", "remote.git")
	for _, name := range names {
		dir := inDir(t, root, name)
		gitIn(t, dir, "init", "-q", ".")
		gitIn(t, dir, "remote", "add", "origin", filepath.Join(root, "remote.git"))
		if r := tess(t, "init", "--prefix", "t"); r.code != 0 {
			t.Fatalf("tess init in %s: %s", name, r.stderr)
		}
	}
	return root
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// inCopy moves the test into a copy and sets the clock.
func inCopy(t *testing.T, root, name, now string) {
	t.Helper()
	t.Chdir(filepath.Join(root, name))
	t.Setenv("TESS_NOW", now)
}

// must runs tess and fails the test unless it exits 0.
func must(t *testing.T, args ...string) result {
	t.Helper()
	r := tess(t, args...)
	if r.code != 0 {
		t.Fatalf("tess %s: exit %d\n%s", strings.Join(args, " "), r.code, r.stderr)
	}
	return r
}

// titleConflict leaves copy a about to sync a title that copy b changed
// five seconds later and has already pushed. It returns the root and the ID.
func titleConflict(t *testing.T) (root, id string) {
	t.Helper()
	root = copies(t, "a", "b")
	inCopy(t, root, "a", "2026-01-01T00:00:00Z")
	id = created(t, "Shared")["id"].(string)
	must(t, "sync")
	inCopy(t, root, "b", "2026-01-01T00:00:00Z")
	must(t, "sync")
	inCopy(t, root, "a", "2026-01-02T00:00:00Z")
	must(t, "update", id, "--title", "From a")
	inCopy(t, root, "b", "2026-01-02T00:00:05Z")
	must(t, "update", id, "--title", "From b")
	must(t, "sync")
	inCopy(t, root, "a", "2026-01-02T00:01:00Z")
	return root, id
}

func TestSyncWithoutARemoteIsNoRemote(t *testing.T) {
	inInitialisedRepo(t)
	wantError(t, tess(t, "sync", "--json"), "no_remote")
}

func TestSyncBeforeInitIsNotInitialized(t *testing.T) {
	inRepo(t, "repo")
	wantError(t, tess(t, "sync", "--json"), "not_initialized")
}

func TestSyncPushesThenIsUpToDate(t *testing.T) {
	root := copies(t, "a")
	inCopy(t, root, "a", "2026-01-01T00:00:00Z")
	created(t, "Shared")

	r := must(t, "sync", "--json")
	want := `{"remote":"origin","fetched":0,"pushed":1,"dry_run":false,"resolved":[]}` + "\n"
	if r.stdout != want || r.stderr != "" {
		t.Errorf("stdout %q, stderr %q; want %q and nothing", r.stdout, r.stderr, want)
	}

	r = must(t, "sync")
	if r.stdout != "up to date\n" || r.stderr != "" {
		t.Errorf("stdout %q, stderr %q; want up to date and nothing", r.stdout, r.stderr)
	}
}

func TestSyncPrintsWhatItFetchedAndPushed(t *testing.T) {
	root := copies(t, "a", "b")
	inCopy(t, root, "a", "2026-01-01T00:00:00Z")
	created(t, "One")
	created(t, "Two")
	must(t, "sync")
	inCopy(t, root, "b", "2026-01-01T00:00:00Z")
	created(t, "Three")
	r := must(t, "sync")
	if r.stdout != "fetched 2 issues, pushed 1\n" || r.stderr != "" {
		t.Errorf("stdout %q, stderr %q", r.stdout, r.stderr)
	}
}

func TestSyncPrintsAResolutionOnStderrAndTheSummaryOnStdout(t *testing.T) {
	_, id := titleConflict(t)
	r := must(t, "sync")
	want := "resolved " + id + ` title: kept "From b" (other copy, 2026-01-02T00:00:05Z), dropped "From a" (this copy, 2026-01-02T00:00:00Z)` + "\n"
	if r.stderr != want {
		t.Errorf("stderr:\n got %q\nwant %q", r.stderr, want)
	}
	if r.stdout != "fetched 1 issue, pushed 1, resolved 1\n" {
		t.Errorf("stdout %q", r.stdout)
	}
}

func TestSyncJSONCarriesTheResolutionAndNothingOnStderr(t *testing.T) {
	_, id := titleConflict(t)
	r := must(t, "sync", "--json")
	want := `{"remote":"origin","fetched":1,"pushed":1,"dry_run":false,"resolved":[` +
		`{"issue":"` + id + `","field":"title","kept":"From b","dropped":"From a","won":"remote","reason":"later",` +
		`"kept_at":"2026-01-02T00:00:05Z","dropped_at":"2026-01-02T00:00:00Z"}]}` + "\n"
	if r.stdout != want || r.stderr != "" {
		t.Errorf("stdout:\n got %q\nwant %q\nstderr %q", r.stdout, want, r.stderr)
	}
}

func TestSyncDryRunSaysWhatItWouldMoveAndMovesNothing(t *testing.T) {
	root := copies(t, "a", "b")
	inCopy(t, root, "a", "2026-01-01T00:00:00Z")
	id := created(t, "From a")["id"].(string)
	must(t, "sync")
	inCopy(t, root, "b", "2026-01-01T00:00:00Z")
	created(t, "From b")

	r := must(t, "sync", "--dry-run")
	if r.stdout != "would push 1, would fetch 1\n" || r.stderr != "" {
		t.Errorf("stdout %q, stderr %q", r.stdout, r.stderr)
	}
	r = must(t, "sync", "--dry-run", "--json")
	want := `{"remote":"origin","fetched":1,"pushed":1,"dry_run":true,"resolved":[]}` + "\n"
	if r.stdout != want {
		t.Errorf("stdout %q, want %q", r.stdout, want)
	}
	// Nothing came in, and the real sync still has it all to do.
	if r := tess(t, "show", id, "--json"); r.code == 0 {
		t.Errorf("the dry run fetched %s into the database", id)
	}
	if r := must(t, "sync"); r.stdout != "fetched 1 issue, pushed 1\n" {
		t.Errorf("after the dry runs: stdout %q", r.stdout)
	}
}

func TestSyncRemoteNamesTheRemote(t *testing.T) {
	root := copies(t, "a")
	gitIn(t, root, "init", "-q", "--bare", "backup.git")
	gitIn(t, filepath.Join(root, "a"), "remote", "add", "backup", filepath.Join(root, "backup.git"))
	inCopy(t, root, "a", "2026-01-01T00:00:00Z")
	created(t, "Shared")
	must(t, "sync")

	// origin has the issue and backup does not: only the named one is asked.
	r := must(t, "sync", "--remote", "backup", "--json")
	got := decode[map[string]any](t, r.stdout)
	if got["remote"] != "backup" {
		t.Errorf("remote = %v, want backup", got["remote"])
	}
	wantError(t, tess(t, "sync", "--remote", "nowhere", "--json"), "no_remote")
}

func TestSyncTakesNoArguments(t *testing.T) {
	inInitialisedRepo(t)
	r := tess(t, "sync", "origin")
	if r.code != 1 || !strings.Contains(r.stderr, "usage: tess sync [--remote NAME] [--dry-run]") {
		t.Errorf("exit %d, stderr %q", r.code, r.stderr)
	}
}

func TestSyncPrintsAnEditDroppedBecauseTheIssueWasRetired(t *testing.T) {
	root := copies(t, "a", "b")
	inCopy(t, root, "a", "2026-01-01T00:00:00Z")
	id := created(t, "To retire")["id"].(string)
	must(t, "sync")
	inCopy(t, root, "b", "2026-01-01T00:00:00Z")
	must(t, "sync")
	inCopy(t, root, "a", "2026-01-02T00:01:00Z")
	must(t, "retire", id)
	must(t, "sync")
	inCopy(t, root, "b", "2026-01-02T00:02:00Z")
	must(t, "update", id, "--title", "Edited after the retire", "--add-label", "late")

	r := must(t, "sync")
	want := "dropped " + id + " title: issue was retired at 2026-01-02T00:01:00Z\n" +
		"dropped " + id + " labels late: issue was retired at 2026-01-02T00:01:00Z\n"
	if r.stderr != want {
		t.Errorf("stderr:\n got %q\nwant %q", r.stderr, want)
	}
	if !strings.HasSuffix(r.stdout, ", resolved 2\n") {
		t.Errorf("stdout %q", r.stdout)
	}
}

func TestSyncPrintsALinkDroppedToAvoidALoop(t *testing.T) {
	root := copies(t, "a", "b")
	inCopy(t, root, "a", "2026-01-01T00:00:00Z")
	x := created(t, "X")["id"].(string)
	y := created(t, "Y")["id"].(string)
	must(t, "sync")
	inCopy(t, root, "b", "2026-01-01T00:00:00Z")
	must(t, "sync")
	inCopy(t, root, "a", "2026-01-02T00:01:00Z")
	must(t, "link", x, "blocks", y)
	must(t, "sync")
	inCopy(t, root, "b", "2026-01-02T00:02:00Z")
	must(t, "link", y, "blocks", x)

	// In JSON a dropped link has no kept value and no kept time.
	r := must(t, "sync", "--dry-run", "--json")
	got := decode[syncResult](t, r.stdout)
	if len(got.Resolved) != 1 {
		t.Fatalf("resolved = %s, want one", r.stdout)
	}
	res := got.Resolved[0]
	if res.Reason != "cycle" || res.Kept != nil || res.KeptAt != nil || res.Dropped != y ||
		res.DroppedAt == nil || *res.DroppedAt != "2026-01-02T00:02:00Z" {
		t.Errorf("resolution = %s", r.stdout)
	}
	if !strings.Contains(r.stdout, `"kept":null`) || !strings.Contains(r.stdout, `"kept_at":null`) {
		t.Errorf("no null kept and kept_at in %s", r.stdout)
	}

	r = must(t, "sync")
	if want := "dropped " + x + " blocked_by " + y + ": would make a dependency loop\n"; r.stderr != want {
		t.Errorf("stderr:\n got %q\nwant %q", r.stderr, want)
	}
}

func TestSyncPrintsAStatusBareAndAPriorityAsANumber(t *testing.T) {
	root := copies(t, "a", "b")
	inCopy(t, root, "a", "2026-01-01T00:00:00Z")
	id := created(t, "Shared")["id"].(string)
	must(t, "sync")
	inCopy(t, root, "b", "2026-01-01T00:00:00Z")
	must(t, "sync")
	inCopy(t, root, "a", "2026-01-02T00:00:00Z")
	must(t, "status", id, "in-progress")
	must(t, "update", id, "-p", "0")
	inCopy(t, root, "b", "2026-01-02T00:00:05Z")
	must(t, "status", id, "done")
	must(t, "update", id, "-p", "1")
	must(t, "sync")
	inCopy(t, root, "a", "2026-01-02T00:01:00Z")

	r := must(t, "sync")
	want := "resolved " + id + " priority: kept 1 (other copy, 2026-01-02T00:00:05Z), dropped 0 (this copy, 2026-01-02T00:00:00Z)\n" +
		"resolved " + id + " status: kept done (other copy, 2026-01-02T00:00:05Z), dropped in-progress (this copy, 2026-01-02T00:00:00Z)\n"
	if r.stderr != want {
		t.Errorf("stderr:\n got %q\nwant %q", r.stderr, want)
	}
}

func TestAFailedSyncIsARefusalWithItsCodeAndHint(t *testing.T) {
	root := copies(t, "a")
	inCopy(t, root, "a", "2026-01-01T00:00:00Z")
	gitIn(t, filepath.Join(root, "a"), "remote", "set-url", "origin", filepath.Join(root, "nowhere.git"))
	wantError(t, tess(t, "sync", "--json"), "offline")
	r := tess(t, "sync")
	if r.code != 1 || r.stdout != "" || !strings.HasPrefix(r.stderr, "error: ") || strings.Contains(r.stderr, "usage:") {
		t.Errorf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
}
