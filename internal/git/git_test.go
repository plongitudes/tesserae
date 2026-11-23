package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/plongitudes/tesserae/internal/tracker"
)

var ctx = context.Background()

// run runs git in dir for a test's setup and returns its trimmed output.
func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRepo makes a repository with a working tree under the test's temp
// directory (which is under $TMPDIR).
func newRepo(t *testing.T) Repo {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "init", "-q", ".")
	return Repo{Dir: dir}
}

// newBare makes a bare repository and returns its path.
func newBare(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "remote.git")
	run(t, "", "init", "-q", "--bare", dir)
	return dir
}

// realPath resolves symlinks, since macOS temp directories sit behind one.
func realPath(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func wantCode(t *testing.T, err error, code string) *tracker.Error {
	t.Helper()
	var te *tracker.Error
	if !errors.As(err, &te) {
		t.Fatalf("error = %v (%T), want a *tracker.Error with code %s", err, err, code)
	}
	if te.Code != code {
		t.Fatalf("code = %s (%s), want %s", te.Code, te.Message, code)
	}
	return te
}

var (
	alice = Signature{Name: "alice", Email: "alice@example.com", When: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	bob   = Signature{Name: "bob", Email: "", When: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)}
)

// commit writes a commit holding files on top of parents.
func commit(t *testing.T, r Repo, files map[string][]byte, parents ...string) string {
	t.Helper()
	hash, err := r.WriteCommit(ctx, Commit{Parents: parents, Author: alice, Committer: alice, Message: "test", Files: files})
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func TestCommonDirIsTheSameAbsolutePathFromEveryDirectoryOfARepository(t *testing.T) {
	r := newRepo(t)
	sub := filepath.Join(r.Dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(realPath(t, r.Dir), ".git")
	for _, dir := range []string{r.Dir, sub} {
		got, err := Repo{Dir: dir}.CommonDir(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if realPath(t, got) != want || !filepath.IsAbs(got) {
			t.Errorf("CommonDir from %s = %s, want %s", dir, got, want)
		}
	}
}

func TestCommonDirOfALinkedWorktreeIsTheMainRepositorys(t *testing.T) {
	r := newRepo(t)
	run(t, r.Dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "first")
	linked := filepath.Join(t.TempDir(), "linked")
	run(t, r.Dir, "worktree", "add", "-q", linked)
	got, err := Repo{Dir: linked}.CommonDir(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(realPath(t, r.Dir), ".git"); realPath(t, got) != want {
		t.Errorf("CommonDir = %s, want %s", got, want)
	}
}

func TestCommonDirOutsideARepositoryIsNotAGitRepo(t *testing.T) {
	_, err := Repo{Dir: t.TempDir()}.CommonDir(ctx)
	wantCode(t, err, CodeNotAGitRepo)
}

func TestWorktreeRootIsTheTopOfTheCheckout(t *testing.T) {
	r := newRepo(t)
	sub := filepath.Join(r.Dir, "a")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Repo{Dir: sub}.WorktreeRoot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if realPath(t, got) != realPath(t, r.Dir) {
		t.Errorf("WorktreeRoot = %s, want %s", got, r.Dir)
	}
}

func TestWorktreeRootOfABareRepositoryIsErrNoWorktree(t *testing.T) {
	_, err := Repo{Dir: newBare(t)}.WorktreeRoot(ctx)
	if !errors.Is(err, ErrNoWorktree) {
		t.Fatalf("error = %v, want ErrNoWorktree", err)
	}
	_, err = Repo{Dir: t.TempDir()}.WorktreeRoot(ctx)
	wantCode(t, err, CodeNotAGitRepo)
}

func TestResolveRefGivesARefsCommitOrSaysItIsAbsent(t *testing.T) {
	r := newRepo(t)
	hash, ok, err := r.ResolveRef(ctx, "refs/x/one")
	if err != nil || ok || hash != "" {
		t.Fatalf("absent ref: %q, %v, %v; want \"\", false, nil", hash, ok, err)
	}
	c := commit(t, r, map[string][]byte{"f": []byte("1")})
	run(t, r.Dir, "update-ref", "refs/x/one", c)
	hash, ok, err = r.ResolveRef(ctx, "refs/x/one")
	if err != nil || !ok || hash != c {
		t.Fatalf("present ref: %q, %v, %v; want %s, true, nil", hash, ok, err, c)
	}
}

func TestRemotesListsNamesAndURLsSortedByName(t *testing.T) {
	r := newRepo(t)
	remotes, err := r.Remotes(ctx)
	if err != nil || len(remotes) != 0 {
		t.Fatalf("no remotes: %v, %v", remotes, err)
	}
	run(t, r.Dir, "remote", "add", "origin", "/srv/git/a b.git")
	run(t, r.Dir, "remote", "add", "backup.one", "https://example.com/x.git")
	remotes, err = r.Remotes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []Remote{{Name: "backup.one", URL: "https://example.com/x.git"}, {Name: "origin", URL: "/srv/git/a b.git"}}
	if len(remotes) != 2 || remotes[0] != want[0] || remotes[1] != want[1] {
		t.Errorf("Remotes = %v, want %v", remotes, want)
	}
}

func TestUpdateRefMovesOnlyFromTheExpectedValue(t *testing.T) {
	r := newRepo(t)
	one := commit(t, r, map[string][]byte{"f": []byte("1")})
	two := commit(t, r, map[string][]byte{"f": []byte("2")}, one)
	const ref = "refs/x/cas"

	// "" as the expected value means the ref must not exist yet.
	if err := r.UpdateRef(ctx, ref, one, ""); err != nil {
		t.Fatal(err)
	}
	if err := r.UpdateRef(ctx, ref, two, ""); err == nil {
		t.Fatal("creating a ref that exists: no error")
	}
	if err := r.UpdateRef(ctx, ref, one, two); err == nil {
		t.Fatal("moving from the wrong old value: no error")
	}
	if got := run(t, r.Dir, "rev-parse", ref); got != one {
		t.Fatalf("after two refused updates the ref is %s, want %s", got, one)
	}
	if err := r.UpdateRef(ctx, ref, two, one); err != nil {
		t.Fatal(err)
	}
	if got := run(t, r.Dir, "rev-parse", ref); got != two {
		t.Errorf("ref = %s, want %s", got, two)
	}
}

func TestVersionIsTheGitBinarysVersion(t *testing.T) {
	got, err := Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimPrefix(run(t, "", "--version"), "git version ")
	if got != want || got == "" || strings.Contains(got, "git") {
		t.Errorf("Version is %q, want %q", got, want)
	}
}

func TestVersionWithoutGitSaysGitIsMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := Version(ctx); err == nil || !strings.Contains(err.Error(), "can't find it") {
		t.Errorf("error is %v, want it to say git can't be found", err)
	}
}

func TestNoPromptTurnsOffGitsTerminalPromptAndNothingElse(t *testing.T) {
	plain := Repo{}.env()
	for _, v := range plain {
		if strings.HasPrefix(v, "GIT_TERMINAL_PROMPT=") {
			t.Errorf("a plain Repo sets %s", v)
		}
	}
	quiet := Repo{NoPrompt: true}.env()
	if !slices.Contains(quiet, "GIT_TERMINAL_PROMPT=0") {
		t.Errorf("NoPrompt does not set GIT_TERMINAL_PROMPT=0: %v", quiet)
	}
	if !slices.Contains(quiet, "LC_ALL=C") || len(quiet) != len(plain)+1 {
		t.Errorf("NoPrompt changes more than one variable: %d vs %d entries", len(quiet), len(plain))
	}
}

func TestRunDoesNotWaitForAChildThatKeepsGitsPipeOpen(t *testing.T) {
	r := newRepo(t)
	start := time.Now()
	// The alias exits at once; the background sleep inherits git's stdout.
	_, err := r.run(context.Background(), nil, "-c", "alias.linger=!sh -c 'sleep 3 & exit 0'", "linger")
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 2500*time.Millisecond {
		t.Errorf("run took %s: it waited for the child, not just for git", took)
	}
}

func TestUserNameIsGitsConfiguredNameOrEmpty(t *testing.T) {
	r := newRepo(t)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "none"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	name, err := r.UserName(context.Background())
	if err != nil || name != "" {
		t.Errorf("unset: %q, %v; want empty and no error", name, err)
	}
	run(t, r.Dir, "config", "user.name", "Ada Lovelace")
	name, err = r.UserName(context.Background())
	if err != nil || name != "Ada Lovelace" {
		t.Errorf("set: %q, %v", name, err)
	}
}
