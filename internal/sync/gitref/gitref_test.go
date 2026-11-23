package gitref

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/plongitudes/tesserae/internal/git"
	"github.com/plongitudes/tesserae/internal/tracker"
)

var ctx = context.Background()

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

func newBare(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "remote.git")
	run(t, "", "init", "-q", "--bare", dir)
	return dir
}

// newCopy makes a repository whose origin is remote, and its transport.
func newCopy(t *testing.T, remote string) (string, *Transport) {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "init", "-q", ".")
	run(t, dir, "remote", "add", "origin", remote)
	tr, err := Open(ctx, git.Repo{Dir: dir}, "")
	if err != nil {
		t.Fatal(err)
	}
	return dir, tr
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

func files(pairs ...string) map[string][]byte {
	m := map[string][]byte{}
	for i := 0; i < len(pairs); i += 2 {
		m[pairs[i]] = []byte(pairs[i+1])
	}
	return m
}

func by(actor string) Commit {
	return Commit{Actor: actor, When: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Message: "tess sync: test\n"}
}

// state fetches and reads both refs, as a sync starts.
func state(t *testing.T, tr *Transport) (lkg, theirs Tree) {
	t.Helper()
	lkg, err := tr.Local(ctx)
	if err != nil {
		t.Fatal(err)
	}
	theirs, err = tr.Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return lkg, theirs
}

// sync pushes files from a copy and moves its local ref, as a sync ends.
func sync(t *testing.T, tr *Transport, f map[string][]byte) string {
	t.Helper()
	lkg, theirs := state(t, tr)
	commit, err := tr.Push(ctx, lkg, theirs, f, by("tester"))
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Advance(ctx, lkg, commit); err != nil {
		t.Fatal(err)
	}
	return commit
}

func TestChooseTakesTheNamedRemoteThenOriginThenTheOnlyOne(t *testing.T) {
	remotes := func(names ...string) []git.Remote {
		var rs []git.Remote
		for _, n := range names {
			rs = append(rs, git.Remote{Name: n, URL: "/somewhere/" + n})
		}
		return rs
	}
	for _, c := range []struct {
		remotes []git.Remote
		name    string
		want    string // "" for no_remote
	}{
		{remotes("origin", "backup"), "backup", "backup"},
		{remotes("backup", "origin"), "", "origin"},
		{remotes("upstream"), "", "upstream"},
		{remotes(), "", ""},
		{remotes("a", "b"), "", ""},
		{remotes("origin"), "elsewhere", ""},
		{remotes(), "origin", ""},
	} {
		got, err := choose(c.remotes, c.name)
		if c.want == "" {
			wantCode(t, err, CodeNoRemote)
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("choose(%v, %q) = %q, %v; want %q", c.remotes, c.name, got, err, c.want)
		}
	}
}

func TestChooseWithSeveralRemotesHintsAtNamingOne(t *testing.T) {
	_, err := choose([]git.Remote{{Name: "a"}, {Name: "b"}}, "")
	if te := wantCode(t, err, CodeNoRemote); !strings.Contains(te.Hint, "--remote") {
		t.Errorf("hint = %q, want it to name --remote", te.Hint)
	}
}

func TestOpenOutsideARepositoryIsNotAGitRepo(t *testing.T) {
	_, err := Open(ctx, git.Repo{Dir: t.TempDir()}, "")
	wantCode(t, err, git.CodeNotAGitRepo)
}

func TestOpenWithoutARemoteIsNoRemote(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "init", "-q", ".")
	_, err := Open(ctx, git.Repo{Dir: dir}, "")
	wantCode(t, err, CodeNoRemote)
}

func TestBothRefsAreAbsentBeforeTheFirstSync(t *testing.T) {
	_, tr := newCopy(t, newBare(t))
	lkg, theirs := state(t, tr)
	if lkg.Present() || theirs.Present() {
		t.Fatalf("lkg = %+v, theirs = %+v; want both absent", lkg, theirs)
	}
}

func TestPushWritesTheRemoteRefAndNothingLocal(t *testing.T) {
	remote := newBare(t)
	dir, tr := newCopy(t, remote)
	lkg, theirs := state(t, tr)
	want := files("config.json", "{}\n", "issues/t-1.json", "one\n")

	commit, err := tr.Push(ctx, lkg, theirs, want, by("alice"))
	if err != nil {
		t.Fatal(err)
	}
	if got := run(t, remote, "rev-parse", "refs/tess/sync"); got != commit {
		t.Errorf("remote ref = %s, want %s", got, commit)
	}
	if refs := run(t, dir, "for-each-ref"); refs != "" {
		t.Errorf("a push moved local refs:\n%s", refs)
	}
	if got := run(t, remote, "log", "-1", "--format=%an|%cn|%aI|%s", commit); got != "alice|alice|2026-01-02T03:04:05Z|tess sync: test" {
		t.Errorf("commit = %s", got)
	}

	// The copy sees it as theirs on its next fetch; the local ref moves
	// only when told to.
	lkg, theirs = state(t, tr)
	if lkg.Present() {
		t.Errorf("the local ref exists before Advance")
	}
	if theirs.Commit != commit || !reflect.DeepEqual(theirs.Files, want) {
		t.Errorf("fetched = %s %q, want %s %q", theirs.Commit, theirs.Files, commit, want)
	}
	if got := run(t, dir, "rev-parse", "refs/tess/remotes/origin/sync"); got != commit {
		t.Errorf("fetched ref = %s, want %s", got, commit)
	}
	if err := tr.Advance(ctx, lkg, commit); err != nil {
		t.Fatal(err)
	}
	if lkg, _ = state(t, tr); lkg.Commit != commit || !reflect.DeepEqual(lkg.Files, want) {
		t.Errorf("local = %s %q, want %s %q", lkg.Commit, lkg.Files, commit, want)
	}
}

func TestPushMakesACommitWithTheLKGAndTheirsAsParents(t *testing.T) {
	remote := newBare(t)
	_, a := newCopy(t, remote)
	_, b := newCopy(t, remote)
	first := sync(t, a, files("issues/t-1.json", "one\n", "issues/t-2.json", "two\n"))
	if got := sync(t, b, files("issues/t-1.json", "one\n", "issues/t-2.json", "two\n", "issues/t-3.json", "three\n")); got == first {
		t.Fatal("b's sync made no commit")
	}
	second := run(t, remote, "rev-parse", "refs/tess/sync")

	// a's LKG is first, theirs is second. A file gone from the merged
	// tree is deleted, an unchanged one kept, a changed one rewritten.
	lkg, theirs := state(t, a)
	merged := files("issues/t-1.json", "one, edited\n", "issues/t-3.json", "three\n")
	commit, err := a.Push(ctx, lkg, theirs, merged, by("a"))
	if err != nil {
		t.Fatal(err)
	}
	if got := run(t, remote, "log", "-1", "--format=%P", commit); got != first+" "+second {
		t.Errorf("parents = %s, want %s %s", got, first, second)
	}
	if _, got := state(t, b); !reflect.DeepEqual(got.Files, merged) {
		t.Errorf("tree = %q, want %q", got.Files, merged)
	}
}

func TestPushWithOneRefAbsentHasOneParent(t *testing.T) {
	remote := newBare(t)
	_, a := newCopy(t, remote)
	_, b := newCopy(t, remote)
	first := sync(t, a, files("f", "1\n"))

	// b has no LKG: the only parent is theirs.
	lkg, theirs := state(t, b)
	commit, err := b.Push(ctx, lkg, theirs, files("f", "1\n", "g", "2\n"), by("b"))
	if err != nil {
		t.Fatal(err)
	}
	if got := run(t, remote, "log", "-1", "--format=%P", commit); got != first {
		t.Errorf("parents = %s, want %s", got, first)
	}
	if _, got := state(t, a); !reflect.DeepEqual(got.Files, files("f", "1\n", "g", "2\n")) {
		t.Errorf("tree = %q", got.Files)
	}

	// a has an LKG and a new, empty remote has no ref: the only parent is
	// the LKG.
	other := newBare(t)
	run(t, a.repo.Dir, "remote", "add", "other", other)
	toOther, err := Open(ctx, git.Repo{Dir: a.repo.Dir}, "other")
	if err != nil {
		t.Fatal(err)
	}
	lkg, theirs = state(t, toOther)
	if lkg.Commit != first || theirs.Present() {
		t.Fatalf("lkg = %s, theirs = %s", lkg.Commit, theirs.Commit)
	}
	commit, err = toOther.Push(ctx, lkg, theirs, files("f", "1\n"), by("a"))
	if err != nil {
		t.Fatal(err)
	}
	if got := run(t, other, "log", "-1", "--format=%P", commit); got != first {
		t.Errorf("parents = %s, want %s", got, first)
	}
}

func TestPushAfterTheRemoteMovedIsErrRemoteMovedAndMovesNothing(t *testing.T) {
	remote := newBare(t)
	dirA, a := newCopy(t, remote)
	_, b := newCopy(t, remote)
	first := sync(t, a, files("f", "1\n"))

	lkg, theirs := state(t, a)
	second := sync(t, b, files("f", "1\n", "g", "2\n")) // between a's fetch and a's push

	_, err := a.Push(ctx, lkg, theirs, files("f", "from a\n"), by("a"))
	if !errors.Is(err, ErrRemoteMoved) {
		t.Fatalf("Push = %v, want ErrRemoteMoved", err)
	}
	if got := run(t, remote, "rev-parse", "refs/tess/sync"); got != second {
		t.Errorf("remote ref = %s, want b's %s", got, second)
	}
	if got := run(t, dirA, "rev-parse", "refs/tess/sync"); got != first {
		t.Errorf("local ref = %s, want %s", got, first)
	}
}

func TestPushRefusedByTheRemoteIsPushRejected(t *testing.T) {
	remote := newBare(t)
	_, a := newCopy(t, remote)
	hook := filepath.Join(remote, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho 'refused by policy' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	lkg, theirs := state(t, a)
	_, err := a.Push(ctx, lkg, theirs, files("f", "1\n"), by("a"))
	if te := wantCode(t, err, git.CodePushRejected); !strings.Contains(te.Message, "refused by policy") {
		t.Errorf("message = %q, want the remote's reason", te.Message)
	}
}

func TestFetchFromAnUnreachableRemoteIsOffline(t *testing.T) {
	_, a := newCopy(t, filepath.Join(t.TempDir(), "nowhere.git"))
	_, err := a.Fetch(ctx)
	wantCode(t, err, git.CodeOffline)
}

func TestFetchFromARemoteThatLostItsRefIsAbsentNotTheLastFetch(t *testing.T) {
	remote := newBare(t)
	_, a := newCopy(t, remote)
	sync(t, a, files("f", "1\n"))
	run(t, remote, "update-ref", "-d", "refs/tess/sync")
	if _, theirs := state(t, a); theirs.Present() {
		t.Errorf("theirs = %s, want absent", theirs.Commit)
	}
}

func TestAdvanceIsCompareAndSwap(t *testing.T) {
	remote := newBare(t)
	dir, a := newCopy(t, remote)
	first := sync(t, a, files("f", "1\n"))
	lkg, theirs := state(t, a)
	second, err := a.Push(ctx, lkg, theirs, files("f", "2\n"), by("a"))
	if err != nil {
		t.Fatal(err)
	}

	// Something else moved the local ref after this sync read it.
	stale := lkg
	stale.Commit = second
	if err := a.Advance(ctx, stale, first); err == nil {
		t.Error("Advance from a value the ref does not have succeeded")
	}
	if got := run(t, dir, "rev-parse", "refs/tess/sync"); got != first {
		t.Errorf("local ref = %s, want it left at %s", got, first)
	}
	if err := a.Advance(ctx, lkg, second); err != nil {
		t.Fatal(err)
	}
	if got := run(t, dir, "rev-parse", "refs/tess/sync"); got != second {
		t.Errorf("local ref = %s, want %s", got, second)
	}
	// Advancing to where the ref already is needs no git and is fine.
	if lkg, _ = state(t, a); a.Advance(ctx, lkg, second) != nil {
		t.Error("Advance to the same commit failed")
	}
}

func TestReachableAsksWithoutFetching(t *testing.T) {
	remote := newBare(t)
	dir, tr := newCopy(t, remote)
	if err := tr.Reachable(ctx); err != nil {
		t.Errorf("an empty remote is reachable: %v", err)
	}
	if refs := run(t, dir, "for-each-ref"); refs != "" {
		t.Errorf("Reachable made refs:\n%s", refs)
	}
	run(t, dir, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "nowhere.git"))
	wantCode(t, tr.Reachable(ctx), git.CodeOffline)
}

func TestLocalIDsListsTheLocalRefWithoutReadingBlobs(t *testing.T) {
	remote := newBare(t)
	dir, tr := newCopy(t, remote)
	commit, ids, err := LocalIDs(ctx, dir)
	if err != nil || commit != "" || ids != nil {
		t.Fatalf("no local ref yet: %q, %v, %v", commit, ids, err)
	}
	files := map[string][]byte{"config.json": []byte(`{"format":1,"prefix":"t"}` + "\n"), "issues/t-1.json": []byte("one")}
	c, err := tr.Push(ctx, Tree{}, Tree{}, files, by("a"))
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Advance(ctx, Tree{}, c); err != nil {
		t.Fatal(err)
	}
	commit, ids, err = LocalIDs(ctx, dir)
	if err != nil || commit != c || len(ids) != 2 || ids["issues/t-1.json"] != git.BlobHash([]byte("one")) {
		t.Errorf("after advance: %q, %v, %v; want %s and both files' hashes", commit, ids, err, c)
	}
}

func TestOpenReadsTheObjectFormatAndHashesWithIt(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "init", "-q", "--object-format=sha256", ".")
	remote := filepath.Join(t.TempDir(), "remote.git")
	run(t, "", "init", "-q", "--bare", "--object-format=sha256", remote)
	run(t, dir, "remote", "add", "origin", remote)
	tr, err := Open(ctx, git.Repo{Dir: dir}, "")
	if err != nil {
		t.Fatal(err)
	}
	if tr.format != "sha256" {
		t.Errorf("format = %q", tr.format)
	}
	files := map[string][]byte{"issues/t-1.json": []byte("one")}
	c, err := tr.Push(ctx, Tree{}, Tree{}, files, by("a"))
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Advance(ctx, Tree{}, c); err != nil {
		t.Fatal(err)
	}
	_, ids, err := LocalIDs(ctx, dir)
	if err != nil || ids["issues/t-1.json"] != git.HashBlob("sha256", []byte("one")) {
		t.Errorf("ids = %v, %v; want the sha256 blob hash", ids, err)
	}
}
