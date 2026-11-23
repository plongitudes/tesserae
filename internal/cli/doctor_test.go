package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/plongitudes/tesserae/internal/sync"
)

// check is one element of doctor's JSON.
type check struct {
	Check  string  `json:"check"`
	Level  string  `json:"level"`
	Detail string  `json:"detail"`
	Fix    *string `json:"fix"`
}

// doctor runs `tess doctor --json` and returns its checks by name, with
// the names in the order printed.
func doctor(t *testing.T, wantCode int, args ...string) (map[string]check, []string) {
	t.Helper()
	r := tess(t, append([]string{"doctor", "--json"}, args...)...)
	if r.code != wantCode || r.stderr != "" {
		t.Fatalf("tess doctor: exit %d, want %d; stderr %q\n%s", r.code, wantCode, r.stderr, r.stdout)
	}
	byName := map[string]check{}
	var names []string
	for _, c := range decode[[]check](t, r.stdout) {
		byName[c.Check] = c
		names = append(names, c.Check)
	}
	return byName, names
}

func wantLevel(t *testing.T, checks map[string]check, name, level string) check {
	t.Helper()
	c, ok := checks[name]
	if !ok {
		t.Fatalf("no %s check in %v", name, checks)
	}
	if c.Level != level {
		t.Errorf("%s is %s (%s), want %s", name, c.Level, c.Detail, level)
	}
	if hasFix := c.Fix != nil; hasFix != (level == "warn" || level == "fail") && name != "remote" {
		t.Errorf("%s at %s: fix is %v", name, level, c.Fix)
	}
	return c
}

// tessOnPath puts an executable called tess on PATH, as an installed tess
// is, and returns nothing: the check only looks for it.
func tessOnPath(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", filepath.Dir(thisTess(t))+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// thisTess makes an executable called tess in a fresh directory and has
// doctor take it for the binary that is running. The directory is not on
// PATH.
func thisTess(t *testing.T) string {
	t.Helper()
	path := fakeTess(t)
	prev := executable
	executable = func() (string, error) { return path, nil }
	t.Cleanup(func() { executable = prev })
	return path
}

// fixText is a check's fix, "" when there is none.
func fixText(c check) string {
	if c.Fix == nil {
		return ""
	}
	return *c.Fix
}

// fakeTess writes an executable called tess into a fresh directory.
func fakeTess(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tess")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// onlyGitOnPath leaves PATH with git and nothing else, so no tess.
func onlyGitOnPath(t *testing.T) {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.Symlink(git, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
}

var allChecks = []string{"git", "repository", "database", "prefix", "remote", "sync", "hooks", "other_trackers"}

func TestDoctorOutsideARepositoryStopsAtTheRepositoryCheck(t *testing.T) {
	inDir(t, isolate(t), "anywhere")
	checks, names := doctor(t, 1)
	if strings.Join(names, " ") != "git repository" {
		t.Fatalf("checks are %v, want git and repository only", names)
	}
	if c := wantLevel(t, checks, "git", "ok"); !strings.HasPrefix(c.Detail, "git ") {
		t.Errorf("git detail is %q, want the version", c.Detail)
	}
	wantLevel(t, checks, "repository", "fail")
}

func TestDoctorWithoutADatabaseFailsWithTheFix(t *testing.T) {
	inRepo(t, "repo")
	checks, names := doctor(t, 1)
	c := wantLevel(t, checks, "database", "fail")
	if c.Fix == nil || *c.Fix != "tess init" {
		t.Errorf("fix is %v, want tess init", c.Fix)
	}
	// What needs the database is skipped; the rest is still checked.
	if got := strings.Join(names, " "); got != "git repository database remote hooks other_trackers" {
		t.Errorf("checks are %q", got)
	}
	if exists(filepath.Join(".git", databaseName)) {
		t.Error("doctor created a database")
	}
	r := tess(t, "doctor")
	if r.code != 1 || r.stderr != "" {
		t.Fatalf("plain: exit %d, stderr %q", r.code, r.stderr)
	}
	if !regexp.MustCompile(`(?m)^fail  database .*\n      fix: tess init$`).MatchString(r.stdout) {
		t.Errorf("plain output:\n%s", r.stdout)
	}
}

func TestDoctorOnAFreshDatabaseIsOkOrInfo(t *testing.T) {
	dir := inInitialisedRepo(t)
	must(t, "create", "One")
	must(t, "create", "Two")
	checks, names := doctor(t, 0)
	if strings.Join(names, " ") != strings.Join(allChecks, " ") {
		t.Fatalf("checks are %v, want %v", names, allChecks)
	}
	if c := wantLevel(t, checks, "repository", "ok"); !strings.HasSuffix(c.Detail, filepath.Join(filepath.Base(dir), ".git")) {
		t.Errorf("repository detail is %q, want the common directory", c.Detail)
	}
	if c := wantLevel(t, checks, "database", "ok"); !strings.Contains(c.Detail, databaseName) ||
		!strings.Contains(c.Detail, "schema 1") || !strings.HasSuffix(c.Detail, ", 2 issues") {
		t.Errorf("database detail is %q, want the path, the schema version and the issue count", c.Detail)
	}
	if c := wantLevel(t, checks, "prefix", "ok"); c.Detail != "t" {
		t.Errorf("prefix detail is %q", c.Detail)
	}
	wantLevel(t, checks, "remote", "info")
	if c := wantLevel(t, checks, "sync", "info"); c.Detail != "never synced" {
		t.Errorf("sync detail is %q", c.Detail)
	}
	wantLevel(t, checks, "hooks", "info")
	wantLevel(t, checks, "other_trackers", "ok")
}

func TestDoctorPlainOutputIsAlignedAsTheContractShows(t *testing.T) {
	inInitialisedRepo(t)
	r := must(t, "doctor")
	if r.stderr != "" {
		t.Errorf("stderr: %q", r.stderr)
	}
	lines := strings.Split(strings.TrimSuffix(r.stdout, "\n"), "\n")
	want := []string{
		`^ok    git           git \d`,
		`^ok    repository    /.*\.git$`,
		`^ok    database      /.*tess\.db, schema \d+, 0 issues$`,
		`^ok    prefix        t$`,
		`^info  remote        no remote configured$`,
		`^      fix: git remote add origin URL, then tess sync$`,
		`^info  sync          never synced$`,
		`^info  hooks         not set up; tess setup claude installs them$`,
		`^ok    other_trackers  none found$`,
	}
	if len(lines) != len(want) {
		t.Fatalf("%d lines, want %d:\n%s", len(lines), len(want), r.stdout)
	}
	for i, re := range want {
		if !regexp.MustCompile(re).MatchString(lines[i]) {
			t.Errorf("line %d is %q, want %s", i+1, lines[i], re)
		}
	}
}

func TestDoctorRemoteIsOkWhenConfiguredAndTestedOnlyWhenAsked(t *testing.T) {
	root := copies(t, "a")
	checks, _ := doctor(t, 0)
	if c := wantLevel(t, checks, "remote", "ok"); !strings.HasPrefix(c.Detail, "origin") {
		t.Errorf("remote detail is %q, want the remote's name", c.Detail)
	}
	checks, _ = doctor(t, 0, "--remote")
	if c := wantLevel(t, checks, "remote", "ok"); !strings.Contains(c.Detail, "reachable") {
		t.Errorf("remote detail is %q, want it to say the remote was reached", c.Detail)
	}

	gitIn(t, filepath.Join(root, "a"), "remote", "set-url", "origin", filepath.Join(root, "nowhere.git"))
	checks, _ = doctor(t, 0)
	wantLevel(t, checks, "remote", "ok") // not tested, so nothing is known against it
	checks, _ = doctor(t, 0, "--remote") // a warning, and still exit 0
	wantLevel(t, checks, "remote", "warn")
}

func TestDoctorWithSeveralRemotesAndNoOriginSaysToNameOne(t *testing.T) {
	dir := inInitialisedRepo(t)
	gitIn(t, dir, "remote", "add", "one", filepath.Join(dir, "one.git"))
	gitIn(t, dir, "remote", "add", "two", filepath.Join(dir, "two.git"))
	checks, _ := doctor(t, 0)
	c := wantLevel(t, checks, "remote", "warn")
	if c.Fix == nil || !strings.Contains(*c.Fix, "--remote") {
		t.Errorf("fix is %v, want it to name --remote", c.Fix)
	}
}

func TestDoctorSyncSaysWhenAndHowManyIssuesChangedSince(t *testing.T) {
	root := copies(t, "a")
	inCopy(t, root, "a", "2026-01-01T00:00:00Z")
	must(t, "create", "First")
	must(t, "sync")
	checks, _ := doctor(t, 0)
	if c := wantLevel(t, checks, "sync", "ok"); c.Detail != "last synced 2026-01-01T00:00:00Z, no issues changed since" {
		t.Errorf("sync detail is %q", c.Detail)
	}
	must(t, "create", "Second")
	checks, _ = doctor(t, 0)
	if c := wantLevel(t, checks, "sync", "ok"); !strings.HasSuffix(c.Detail, ", 1 issue changed since") {
		t.Errorf("sync detail is %q", c.Detail)
	}
	must(t, "create", "Third")
	checks, _ = doctor(t, 0)
	if c := wantLevel(t, checks, "sync", "ok"); !strings.HasSuffix(c.Detail, ", 2 issues changed since") {
		t.Errorf("sync detail is %q", c.Detail)
	}
}

func TestDoctorSyncWarnsAfterAFailedSyncAndNamesTheCode(t *testing.T) {
	root := copies(t, "a")
	inCopy(t, root, "a", "2026-01-01T00:00:00Z")
	must(t, "sync")
	gitIn(t, filepath.Join(root, "a"), "remote", "set-url", "origin", filepath.Join(root, "nowhere.git"))
	if r := tess(t, "sync"); r.code != 1 {
		t.Fatalf("sync to nowhere: exit %d", r.code)
	}
	checks, _ := doctor(t, 0)
	c := wantLevel(t, checks, "sync", "warn")
	if !strings.Contains(c.Detail, "offline") || !strings.Contains(c.Detail, "2026-01-01T00:00:00Z") {
		t.Errorf("sync detail is %q, want the code and the last sync that worked", c.Detail)
	}
}

func TestDoctorSyncWarnsWhenTheLocalRefIsGone(t *testing.T) {
	root := copies(t, "a")
	inCopy(t, root, "a", "2026-01-01T00:00:00Z")
	must(t, "create", "First")
	must(t, "sync")
	gitIn(t, filepath.Join(root, "a"), "update-ref", "-d", "refs/tess/sync")
	checks, _ := doctor(t, 0)
	c := wantLevel(t, checks, "sync", "warn")
	if !strings.Contains(c.Detail, "2026-01-01T00:00:00Z") || !strings.Contains(c.Detail, "snapshot") {
		t.Errorf("sync detail is %q, want the last sync and the lost snapshot", c.Detail)
	}
}

func TestDoctorSyncSaysWhenASyncIsRunningNow(t *testing.T) {
	root := copies(t, "a")
	inCopy(t, root, "a", "2026-01-01T00:00:00Z")
	must(t, "sync")
	release, err := sync.Lock(context.Background(), filepath.Join(root, "a", ".git", "tess.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	checks, _ := doctor(t, 0)
	c := wantLevel(t, checks, "sync", "ok")
	if want := fmt.Sprintf("a sync is running now (pid %d, for ", os.Getpid()); !strings.Contains(c.Detail, want) {
		t.Errorf("sync detail is %q, want it to say %q", c.Detail, want)
	}
	release()
	checks, _ = doctor(t, 0)
	if c := wantLevel(t, checks, "sync", "ok"); strings.Contains(c.Detail, "running") {
		t.Errorf("sync detail still says a sync is running: %q", c.Detail)
	}
}

func TestRunningSyncNoteWarnsWhenItHasRunTooLong(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 10, 0, 0, time.UTC)
	fresh := &sync.LockHolder{PID: 4812, Since: now.Add(-45 * time.Second)}
	if note, stuck := runningNote(fresh, now); note != "a sync is running now (pid 4812, for 45s)" || stuck {
		t.Errorf("fresh: %q stuck=%v", note, stuck)
	}
	old := &sync.LockHolder{PID: 4812, Since: now.Add(-7 * time.Minute)}
	if note, stuck := runningNote(old, now); note != "a sync has been running for 7m0s (pid 4812)" || !stuck {
		t.Errorf("old: %q stuck=%v", note, stuck)
	}
	if note, stuck := runningNote(nil, now); note != "" || stuck {
		t.Errorf("none: %q stuck=%v", note, stuck)
	}
}

func TestDoctorNeverSyncedWithoutARemoteStaysInfoAfterATry(t *testing.T) {
	inInitialisedRepo(t)
	tess(t, "sync") // no_remote: the remote check already says so
	checks, _ := doctor(t, 0)
	wantLevel(t, checks, "sync", "info")
}

func TestDoctorHooksAreOkOnceSetUpAndTessIsOnPath(t *testing.T) {
	inInitialisedRepo(t)
	must(t, "setup", "claude")
	onlyGitOnPath(t)
	checks, _ := doctor(t, 0)
	if c := wantLevel(t, checks, "hooks", "warn"); !strings.Contains(c.Detail, "PATH") {
		t.Errorf("hooks detail is %q, want it to say tess is not on PATH", c.Detail)
	}
	tessOnPath(t)
	checks, _ = doctor(t, 0)
	wantLevel(t, checks, "hooks", "ok")
}

func TestDoctorHooksNameThisBinaryDirectoryWhenNoTessIsOnPath(t *testing.T) {
	inInitialisedRepo(t)
	must(t, "setup", "claude")
	onlyGitOnPath(t)
	self := thisTess(t)
	checks, _ := doctor(t, 0)
	c := wantLevel(t, checks, "hooks", "warn")
	if !strings.Contains(c.Detail, "PATH") || !strings.Contains(fixText(c), filepath.Dir(self)) {
		t.Errorf("hooks = %q fix %q, want the directory of the running binary in the fix", c.Detail, fixText(c))
	}
}

func TestDoctorHooksWarnWhenPathWouldRunADifferentTess(t *testing.T) {
	inInitialisedRepo(t)
	must(t, "setup", "claude")
	onlyGitOnPath(t)
	self := thisTess(t)
	other := fakeTess(t)
	t.Setenv("PATH", filepath.Dir(other)+string(os.PathListSeparator)+os.Getenv("PATH"))
	checks, _ := doctor(t, 0)
	c := wantLevel(t, checks, "hooks", "warn")
	if !strings.Contains(c.Detail, other) || !strings.Contains(c.Detail, self) {
		t.Errorf("hooks detail is %q, want both %s and %s", c.Detail, other, self)
	}
	if !strings.Contains(fixText(c), filepath.Dir(self)) {
		t.Errorf("fix is %q, want the running binary's directory", fixText(c))
	}

	// A symlink to the running binary is the same binary.
	link := filepath.Join(t.TempDir(), "tess")
	if err := os.Symlink(self, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(link)+string(os.PathListSeparator)+os.Getenv("PATH"))
	checks, _ = doctor(t, 0)
	wantLevel(t, checks, "hooks", "ok")
}

func TestDoctorReadsTheUserSettingsFromClaudeConfigDir(t *testing.T) {
	inInitialisedRepo(t)
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	user := filepath.Join(dir, "settings.json")
	writeFile(t, user, `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"bd prime"}]}]}}`)
	checks, _ := doctor(t, 0)
	if c := wantLevel(t, checks, "other_trackers", "warn"); !strings.Contains(c.Detail, "bd prime") || !strings.Contains(c.Detail, user) {
		t.Errorf("detail is %q, want the file under CLAUDE_CONFIG_DIR", c.Detail)
	}
	if got := fileText(t, user); !strings.Contains(got, "bd prime") {
		t.Errorf("doctor changed the user settings:\n%s", got)
	}
}

func TestDoctorReadsTheLocalSettingsToo(t *testing.T) {
	inInitialisedRepo(t)
	if err := os.Mkdir(".claude", 0o755); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(".claude", "settings.local.json")
	writeFile(t, local, `{"hooks":{"SessionEnd":[{"hooks":[{"type":"command","command":"beads sync"}]}]}}`)
	checks, _ := doctor(t, 0)
	if c := wantLevel(t, checks, "other_trackers", "warn"); !strings.Contains(c.Detail, "beads sync") || !strings.Contains(c.Detail, ".claude/settings.local.json") {
		t.Errorf("detail is %q, want the local file and the command", c.Detail)
	}
	// The local file is personal: the hooks check does not look for tess's hooks there.
	wantLevel(t, checks, "hooks", "info")
}

func TestDoctorHooksWarnWhenOnlyOneOfTheTwoIsThere(t *testing.T) {
	inInitialisedRepo(t)
	tessOnPath(t)
	if err := os.Mkdir(".claude", 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, settingsFile, `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"tess hook session-start"}]}]}}`)
	checks, _ := doctor(t, 0)
	c := wantLevel(t, checks, "hooks", "warn")
	if c.Fix == nil || *c.Fix != "tess setup claude" {
		t.Errorf("fix is %v", c.Fix)
	}
}

func TestDoctorNamesAnotherTrackersDirectoryAndHooksAndChangesNothing(t *testing.T) {
	root := filepath.Dir(inInitialisedRepo(t))
	if err := os.Mkdir(".beads", 0o755); err != nil {
		t.Fatal(err)
	}
	checks, _ := doctor(t, 0)
	if c := wantLevel(t, checks, "other_trackers", "warn"); !strings.Contains(c.Detail, ".beads") {
		t.Errorf("detail is %q, want it to name .beads", c.Detail)
	}
	if err := os.Remove(".beads"); err != nil {
		t.Fatal(err)
	}

	const beads = `{"hooks":{"SessionEnd":[{"hooks":[{"type":"command","command":"bd sync"}]}]}}`
	if err := os.Mkdir(".claude", 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, settingsFile, beads)
	checks, _ = doctor(t, 0)
	if c := wantLevel(t, checks, "other_trackers", "warn"); !strings.Contains(c.Detail, "bd sync") ||
		!strings.Contains(c.Detail, settingsFile) || !strings.Contains(c.Detail, "SessionEnd") {
		t.Errorf("detail is %q, want the file, the event and the command", c.Detail)
	}
	if got := fileText(t, settingsFile); got != beads {
		t.Errorf("doctor changed the settings:\n%s", got)
	}

	// The user's own settings are read too.
	writeFile(t, settingsFile, `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"echo abducted"}]}]}}`)
	checks, _ = doctor(t, 0)
	wantLevel(t, checks, "other_trackers", "ok") // "bd" inside a word is no tracker
	home := filepath.Join(root, ".claude")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	user := filepath.Join(home, "settings.json")
	writeFile(t, user, `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"/opt/beads/bin/beads prime"}]}]}}`)
	checks, _ = doctor(t, 0)
	if c := wantLevel(t, checks, "other_trackers", "warn"); !strings.Contains(c.Detail, "beads prime") ||
		!strings.Contains(c.Detail, "~/.claude/settings.json") {
		t.Errorf("detail is %q, want the user's file and the command", c.Detail)
	}
}

func TestDoctorIgnoresOtherTrackersHooksOnOtherEvents(t *testing.T) {
	inInitialisedRepo(t)
	if err := os.Mkdir(".claude", 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, settingsFile, `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"bd lint"}]}]}}`)
	checks, _ := doctor(t, 0)
	wantLevel(t, checks, "other_trackers", "ok")
}

func TestDoctorSaysSoWhenTheSettingsCannotBeRead(t *testing.T) {
	inInitialisedRepo(t)
	if err := os.Mkdir(".claude", 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, settingsFile, `{ not json`)
	checks, _ := doctor(t, 0)
	wantLevel(t, checks, "hooks", "warn")
	if c := wantLevel(t, checks, "other_trackers", "warn"); !strings.Contains(c.Detail, settingsFile) {
		t.Errorf("detail is %q, want the file that can't be read", c.Detail)
	}
}

func TestDoctorTakesNoArguments(t *testing.T) {
	inInitialisedRepo(t)
	wantError(t, tess(t, "doctor", "origin", "--json"), "invalid_argument")
}
