package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// result is what one run of tess left behind.
type result struct {
	code           int
	stdout, stderr string
}

// isolate gives the test its own HOME and git identity, a fixed clock, and
// no inherited actor variables, as the acceptance harness does.
func isolate(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitconfig := filepath.Join(root, ".gitconfig")
	cfg := "[user]\n\tname = tester\n\temail = tester@example.com\n[init]\n\tdefaultBranch = main\n"
	if err := os.WriteFile(gitconfig, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", root)
	t.Setenv("GIT_CONFIG_GLOBAL", gitconfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("TESS_NOW", "2026-01-01T00:00:00Z")
	for _, v := range []string{"TESS_ACTOR", "CLAUDECODE", "CLAUDE_CODE_SESSION_ID"} {
		t.Setenv(v, "")
	}
	// git reads an empty GIT_DIR as a path, so these two have to be absent.
	for _, v := range []string{"GIT_DIR", "GIT_WORK_TREE"} {
		t.Setenv(v, "") // registers the restore
		os.Unsetenv(v)
	}
	return root
}

// inDir makes a directory under the isolated root and moves the test into it.
func inDir(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	return dir
}

// inRepo moves the test into a fresh git repository called name.
func inRepo(t *testing.T, name string) string {
	t.Helper()
	dir := inDir(t, isolate(t), name)
	if out, err := exec.Command("git", "init", "-q", ".").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return dir
}

// inInitialisedRepo is inRepo followed by `tess init --prefix t`.
func inInitialisedRepo(t *testing.T) string {
	t.Helper()
	dir := inRepo(t, "repo")
	if r := tess(t, "init", "--prefix", "t"); r.code != 0 {
		t.Fatalf("tess init: exit %d\n%s", r.code, r.stderr)
	}
	return dir
}

func tess(t *testing.T, args ...string) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, strings.NewReader(""), &stdout, &stderr)
	return result{code, stdout.String(), stderr.String()}
}

func decode[T any](t *testing.T, text string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("not JSON: %v\n%q", err, text)
	}
	return v
}

// wantError checks a --json failure: exit 1, nothing on stdout, and stderr
// holding only the error envelope with the given code.
func wantError(t *testing.T, r result, code string) map[string]any {
	t.Helper()
	if r.code != 1 {
		t.Errorf("exit %d, want 1", r.code)
	}
	if r.stdout != "" {
		t.Errorf("stdout is %q, want nothing", r.stdout)
	}
	envelope := decode[map[string]map[string]any](t, r.stderr)
	e, ok := envelope["error"]
	if !ok || len(envelope) != 1 {
		t.Fatalf("stderr is not an error envelope: %s", r.stderr)
	}
	if e["code"] != code {
		t.Errorf("code %v, want %s (%s)", e["code"], code, r.stderr)
	}
	if msg, _ := e["message"].(string); msg == "" {
		t.Errorf("no message in %s", r.stderr)
	}
	if _, ok := e["hint"]; !ok || len(e) != 3 {
		t.Errorf("envelope keys are not code, message, hint: %s", r.stderr)
	}
	return e
}

// plumbing are the commands the orientation leaves out: run once, or run by
// something other than a person at work on issues.
var plumbing = []string{"init", "import", "setup", "hook"}

func TestNoArgumentsPrintsOneLinePerWorkingCommandAndNoPlumbing(t *testing.T) {
	inDir(t, isolate(t), "anywhere") // not a git repository: orientation needs none
	r := tess(t)
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	lines := strings.Split(r.stdout, "\n")
	for _, name := range []string{"ready", "create", "show", "update", "status", "comment", "link", "unlink", "list", "retire", "restore", "sync"} {
		n := 0
		for _, line := range lines {
			if strings.HasPrefix(line, "  tess "+name+" ") {
				n++
			}
		}
		if n != 1 {
			t.Errorf("orientation has %d lines for tess %s, want 1:\n%s", n, name, r.stdout)
		}
	}
	for _, name := range plumbing {
		if strings.Contains(r.stdout, "tess "+name) {
			t.Errorf("orientation names tess %s:\n%s", name, r.stdout)
		}
	}
	for _, want := range []string{"--json", "--help"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("orientation doesn't name %q:\n%s", want, r.stdout)
		}
	}
	for _, line := range lines {
		if len([]rune(line)) > 80 {
			t.Errorf("orientation line is wider than 80 columns: %q", line)
		}
	}
	if n := strings.Count(r.stdout, "\n"); n > 18 {
		t.Errorf("orientation is %d lines, want one per working command and little else:\n%s", n, r.stdout)
	}
}

// A command added later has to be given a line or be called plumbing.
func TestEveryCommandIsInTheOrientationOrIsPlumbing(t *testing.T) {
	inDir(t, isolate(t), "anywhere")
	out := tess(t).stdout
	a := &app{stdin: strings.NewReader(""), stdout: io.Discard, stderr: io.Discard}
	for _, cmd := range a.rootCommand().Commands() {
		named := strings.Contains(out, "\n  tess "+cmd.Name()+" ")
		if named == slices.Contains(plumbing, cmd.Name()) {
			t.Errorf("tess %s: in the orientation %v, listed as plumbing %v; want exactly one", cmd.Name(), named, named)
		}
	}
}

func TestInitCreatesTheDatabaseInTheGitCommonDir(t *testing.T) {
	dir := inRepo(t, "repo")
	r := tess(t, "init", "--prefix", "t", "--json")
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	got := decode[map[string]string](t, r.stdout)
	if got["prefix"] != "t" || len(got) != 2 {
		t.Errorf("got %s, want prefix t and database", r.stdout)
	}
	db := got["database"]
	if !filepath.IsAbs(db) || filepath.Base(db) != "tess.db" || filepath.Base(filepath.Dir(db)) != ".git" {
		t.Errorf("database is %q, want an absolute path to .git/tess.db", db)
	}
	if _, err := os.Stat(db); err != nil {
		t.Errorf("database file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "tess.db")); err != nil {
		t.Errorf("database is not in this repository's .git: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("the working tree has %d entries, want only .git", len(entries))
	}
}

func TestInitInASubdirectoryUsesTheSameDatabase(t *testing.T) {
	dir := inInitialisedRepo(t)
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	wantError(t, tess(t, "init", "--json"), "already_initialized")
}

func TestInitTwiceIsAlreadyInitialized(t *testing.T) {
	inInitialisedRepo(t)
	wantError(t, tess(t, "init", "--prefix", "other", "--json"), "already_initialized")
}

func TestInitDefaultsThePrefixToTheRepoDirectoryName(t *testing.T) {
	for dir, want := range map[string]string{"myproj": "myproj", "My-Proj_2": "myproj2"} {
		t.Run(dir, func(t *testing.T) {
			inRepo(t, dir)
			r := tess(t, "init", "--json")
			if r.code != 0 {
				t.Fatalf("exit %d: %s", r.code, r.stderr)
			}
			if got := decode[map[string]string](t, r.stdout)["prefix"]; got != want {
				t.Errorf("prefix %q, want %q", got, want)
			}
		})
	}
}

func TestInitAsksForAPrefixWhenTheDirectoryNameGivesNone(t *testing.T) {
	inRepo(t, "2024")
	e := wantError(t, tess(t, "init", "--json"), "invalid_argument")
	if hint, _ := e["hint"].(string); !strings.Contains(hint, "--prefix") {
		t.Errorf("hint %q doesn't name --prefix", hint)
	}
}

func TestOutsideAGitRepositoryIsNotAGitRepo(t *testing.T) {
	inDir(t, isolate(t), "plain")
	wantError(t, tess(t, "init", "--prefix", "t", "--json"), "not_a_git_repo")
	wantError(t, tess(t, "list", "--json"), "not_a_git_repo")
}

func TestBeforeInitCommandsSayToRunInit(t *testing.T) {
	dir := inRepo(t, "repo")
	wantError(t, tess(t, "list", "--json"), "not_initialized")
	wantError(t, tess(t, "create", "A title", "--json"), "not_initialized")

	r := tess(t, "list")
	if r.code != 1 || r.stdout != "" {
		t.Errorf("exit %d, stdout %q", r.code, r.stdout)
	}
	if !strings.HasPrefix(r.stderr, "error: ") || !strings.Contains(r.stderr, "tess init") {
		t.Errorf("plain error is %q, want `error: ...` mentioning tess init", r.stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "tess.db")); err == nil {
		t.Error("a refused command created the database")
	}
}

func TestPlainErrorsStartWithErrorAndNeverUseTheOmitSymbol(t *testing.T) {
	inInitialisedRepo(t)
	r := tess(t, "show", "t-zzzzzzzz")
	if r.code != 1 || r.stdout != "" {
		t.Errorf("exit %d, stdout %q", r.code, r.stdout)
	}
	if !regexp.MustCompile(`^error: \S`).MatchString(r.stderr) || strings.Contains(r.stderr, "✗") {
		t.Errorf("stderr is %q", r.stderr)
	}
	if strings.HasPrefix(r.stderr, "{") {
		t.Errorf("plain mode printed JSON: %s", r.stderr)
	}
}

func TestATrackerRefusalKeepsItsCodeInTheEnvelope(t *testing.T) {
	inInitialisedRepo(t)
	e := wantError(t, tess(t, "show", "t-zzzzzzzz", "--json"), "not_found")
	if msg, _ := e["message"].(string); !strings.Contains(msg, "t-zzzzzzzz") {
		t.Errorf("message %q doesn't name the ID", msg)
	}
}

func TestBadInvocationsAreInvalidArgument(t *testing.T) {
	inInitialisedRepo(t)
	for name, args := range map[string][]string{
		"unknown command":           {"frobnicate", "--json"},
		"unknown flag":              {"list", "--frobnicate", "--json"},
		"unknown flag after --json": {"list", "--json", "--frobnicate"},
		"bad flag value":            {"init", "--prefix", "Not Valid", "--json"},
		"flag without its value":    {"--json", "init", "--prefix"},
		"missing title":             {"create", "--json"},
		"blank title":               {"create", "  ", "--json"},
		"too many arguments":        {"sync", "now", "--json"},
		"show without an ID":        {"show", "--json"},
	} {
		t.Run(name, func(t *testing.T) {
			wantError(t, tess(t, args...), "invalid_argument")
		})
	}
}

func TestAnUnknownCommandInPlainModeIsOneErrorLineWithoutUsage(t *testing.T) {
	inInitialisedRepo(t)
	r := tess(t, "frobnicate")
	if r.code != 1 || !strings.HasPrefix(r.stderr, "error: ") || strings.Contains(r.stderr, "Usage:") {
		t.Errorf("exit %d, stderr %q", r.code, r.stderr)
	}
	if !strings.Contains(r.stderr, "frobnicate") {
		t.Errorf("the error doesn't name the command: %q", r.stderr)
	}
}

func TestSyncIsAStubThatSaysSoOnStderr(t *testing.T) {
	inInitialisedRepo(t)
	r := tess(t, "sync")
	if r.code != 0 || r.stdout != "" || r.stderr != "sync: not available yet\n" {
		t.Errorf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	// With --json, stderr carries only an error object, so the note goes.
	r = tess(t, "sync", "--json")
	if r.code != 0 || r.stderr != "" {
		t.Errorf("--json: exit %d, stderr %q", r.code, r.stderr)
	}
}

func created(t *testing.T, args ...string) map[string]any {
	t.Helper()
	r := tess(t, append([]string{"create", "--json"}, args...)...)
	if r.code != 0 {
		t.Fatalf("create: exit %d: %s", r.code, r.stderr)
	}
	return decode[map[string]any](t, r.stdout)
}

func TestTheActorComesFromTessActorThenClaudeCodeThenGit(t *testing.T) {
	inInitialisedRepo(t)
	if got := created(t, "By git")["created_by"]; got != "tester" {
		t.Errorf("with nothing set: created_by %v, want git's user.name, tester", got)
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", "0123456789abcdef")
	if got := created(t, "Session ID alone")["created_by"]; got != "tester" {
		t.Errorf("a session ID without CLAUDECODE=1: created_by %v, want tester", got)
	}
	t.Setenv("CLAUDECODE", "1")
	if got := created(t, "By Claude")["created_by"]; got != "claude:01234567" {
		t.Errorf("CLAUDECODE=1: created_by %v, want claude:01234567", got)
	}
	t.Setenv("TESS_ACTOR", "alice")
	if got := created(t, "By alice")["created_by"]; got != "alice" {
		t.Errorf("TESS_ACTOR=alice: created_by %v, want alice", got)
	}
}

func TestTessNowIsTheClock(t *testing.T) {
	inInitialisedRepo(t)
	t.Setenv("TESS_NOW", "2026-03-04T05:06:07Z")
	issue := created(t, "Timed")
	if issue["created_at"] != "2026-03-04T05:06:07Z" || issue["updated_at"] != "2026-03-04T05:06:07Z" {
		t.Errorf("created_at %v, updated_at %v; want 2026-03-04T05:06:07Z", issue["created_at"], issue["updated_at"])
	}
	t.Setenv("TESS_NOW", "yesterday")
	wantError(t, tess(t, "create", "Untimed", "--json"), "invalid_argument")
}

func TestCreatePrintsTheIDAndShowReadsTheIssueBack(t *testing.T) {
	inInitialisedRepo(t)
	r := tess(t, "create", "Plain one")
	if r.code != 0 || !regexp.MustCompile(`^t-[0-9a-z]{3,}\n$`).MatchString(r.stdout) {
		t.Fatalf("plain create: exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	id := strings.TrimSpace(r.stdout)
	r = tess(t, "show", id, "--json")
	if r.code != 0 {
		t.Fatalf("show: exit %d: %s", r.code, r.stderr)
	}
	issue := decode[map[string]any](t, r.stdout)
	if issue["id"] != id || issue["title"] != "Plain one" || issue["status"] != "ready" {
		t.Errorf("show gave %s", r.stdout)
	}
	r = tess(t, "list", "--json")
	listed := decode[[]map[string]any](t, r.stdout)
	if len(listed) != 1 || listed[0]["id"] != id {
		t.Errorf("list gave %s", r.stdout)
	}
}

func TestAnEmptyListIsAnEmptyJSONArray(t *testing.T) {
	inInitialisedRepo(t)
	if r := tess(t, "list", "--json"); r.code != 0 || strings.TrimSpace(r.stdout) != "[]" {
		t.Errorf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
}

// A command-line error says how the command is written (cli.md, Output):
// the error, the use line, and where the examples are; no hint line.
func TestCommandLineErrorsPrintUsageAndNoHint(t *testing.T) {
	inInitialisedRepo(t)
	id := strings.TrimSpace(tess(t, "create", "An issue").stdout)
	for name, tc := range map[string]struct {
		args  []string
		usage string // the synopsis, as it follows "usage: "
		help  string // the command whose --help is named
	}{
		"wrong number of arguments":     {[]string{"create"}, "tess create TITLE [-t TYPE] [-p N] [-l LABEL]... [--parent ID] [-b ID]... [-a NAME] [-d TEXT] [--design TEXT] [--acceptance TEXT] [--notes TEXT]", "tess create"},
		"cobra's own argument count":    {[]string{"comment", id}, "tess comment ID TEXT", "tess comment"},
		"no link type":                  {[]string{"link", id}, "tess link ID... TYPE ID...", "tess link"},
		"unknown link type":             {[]string{"unlink", id, "duplicates", id}, "tess unlink ID... TYPE ID...", "tess unlink"},
		"status without a status":       {[]string{"status", id}, "tess status ID... STATUS [-r REASON] [--force]", "tess status"},
		"unknown flag":                  {[]string{"list", "--frobnicate"}, "tess list [--status STATUS] [--type TYPE] [--label LABEL] [--assignee NAME] [--parent ID] [--blocked] [-s TEXT] [--all] [--retired] [-n N] [--flat|--tree]", "tess list"},
		"flag without its value":        {[]string{"list", "-n"}, "tess list [--status STATUS] [--type TYPE] [--label LABEL] [--assignee NAME] [--parent ID] [--blocked] [-s TEXT] [--all] [--retired] [-n N] [--flat|--tree]", "tess list"},
		"bad flag value, by cobra":      {[]string{"ready", "-n", "many"}, "tess ready [-n N] [--flat|--tree]", "tess ready"},
		"bad flag value, by tess":       {[]string{"update", id, "--due", "tomorrow"}, "tess update ID [--title TITLE] [-t TYPE] [-p N] [-a NAME] [--owner NAME] [--due DATE] [--add-label LABEL]... [--remove-label LABEL]... [-d TEXT] [--design TEXT] [--acceptance TEXT] [--notes TEXT] [--append]", "tess update"},
		"unknown field":                 {[]string{"show", id, "--field", "nope"}, "tess show ID... [--history] [--tree]\n   or: tess show ID --field FIELD", "tess show"},
		"--field with two issues":       {[]string{"show", id, id, "--field", "notes"}, "tess show ID... [--history] [--tree]\n   or: tess show ID --field FIELD", "tess show"},
		"flags that exclude each other": {[]string{"list", "--flat", "--tree"}, "tess list [--status STATUS] [--type TYPE] [--label LABEL] [--assignee NAME] [--parent ID] [--blocked] [-s TEXT] [--all] [--retired] [-n N] [--flat|--tree]", "tess list"},
		"nothing to change":             {[]string{"update", id}, "tess update ID [--title TITLE] [-t TYPE] [-p N] [-a NAME] [--owner NAME] [--due DATE] [--add-label LABEL]... [--remove-label LABEL]... [-d TEXT] [--design TEXT] [--acceptance TEXT] [--notes TEXT] [--append]", "tess update"},
		"unknown command":               {[]string{"frobnicate"}, "tess [COMMAND]", "tess"},
		"a command under a command":     {[]string{"setup", "cursor"}, "tess setup claude [--check|--remove]", "tess setup"},
		"a subcommand's arguments":      {[]string{"setup", "claude", "extra"}, "tess setup claude [--check|--remove]", "tess setup claude"},
		"a retire without an issue":     {[]string{"retire"}, "tess retire ID... [-r REASON]", "tess retire"},
		"an init with an argument":      {[]string{"init", "extra"}, "tess init [--prefix PREFIX]", "tess init"},
	} {
		t.Run(name, func(t *testing.T) {
			r := tess(t, tc.args...)
			lines := strings.Split(strings.TrimSuffix(r.stderr, "\n"), "\n")
			if r.code != 1 || r.stdout != "" || len(lines) < 3 {
				t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
			}
			if !strings.HasPrefix(lines[0], "error: ") {
				t.Errorf("first line is %q, want an error: line", lines[0])
			}
			if want := "\nusage: " + tc.usage + "\nrun '" + tc.help + " --help' for examples\n"; !strings.HasSuffix(r.stderr, want) {
				t.Errorf("stderr is %q, want it to end with %q", r.stderr, want)
			}
			if strings.Contains(r.stderr, "hint:") {
				t.Errorf("a command-line error has a hint line: %q", r.stderr)
			}

			e := wantError(t, tess(t, append(tc.args, "--json")...), "invalid_argument")
			if want := strings.ReplaceAll(tc.usage, "\n   or: ", "\n"); e["hint"] != want {
				t.Errorf("JSON hint is %v, want the synopsis %q", e["hint"], want)
			}
		})
	}
}

// What a hint used to carry and a use line can't (the fields, the link
// types, the statuses) is in the message of a command-line error.
func TestCommandLineErrorsNameTheValuesThatWouldWork(t *testing.T) {
	inInitialisedRepo(t)
	id := strings.TrimSpace(tess(t, "create", "An issue").stdout)
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"unknown field":     {[]string{"show", id, "--field", "nope"}, "acceptance_criteria"},
		"unknown link type": {[]string{"link", id, "duplicates", id}, "discovered-from"},
		"no link type":      {[]string{"link", id, id}, "blocked-by"},
		"no status":         {[]string{"status", id}, "pending-review"},
		"bad date":          {[]string{"update", id, "--due", "tomorrow"}, "2026-02-01"},
	} {
		t.Run(name, func(t *testing.T) {
			e := wantError(t, tess(t, append(tc.args, "--json")...), "invalid_argument")
			if msg, _ := e["message"].(string); !strings.Contains(msg, tc.want) {
				t.Errorf("message %q doesn't name %s", msg, tc.want)
			}
		})
	}
}

// A refusal is not a command-line error, even when its code is
// invalid_argument: it keeps its hint and prints no usage.
func TestRefusalsKeepTheirHintAndPrintNoUsage(t *testing.T) {
	inInitialisedRepo(t)
	id := strings.TrimSpace(tess(t, "create", "An issue").stdout)
	for name, args := range map[string][]string{
		"not found":                {"show", "t-zzzzzzzz"},
		"the tracker's own check":  {"update", id, "-p", "9"},
		"a file that isn't there":  {"import", "missing.jsonl"},
		"a status that needs more": {"status", id, "held"},
	} {
		t.Run(name, func(t *testing.T) {
			r := tess(t, args...)
			if r.code != 1 || !strings.HasPrefix(r.stderr, "error: ") {
				t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
			}
			if !strings.Contains(r.stderr, "\nhint: ") {
				t.Errorf("a refusal lost its hint: %q", r.stderr)
			}
			if strings.Contains(r.stderr, "usage:") || strings.Contains(r.stderr, "--help' for examples") {
				t.Errorf("a refusal prints usage: %q", r.stderr)
			}
		})
	}
}
