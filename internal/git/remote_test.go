package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	syncRef    = "refs/x/sync"
	fetchedRef = "refs/x/remotes/origin/sync"
)

func TestPushThenFetchCarriesACommitBetweenRepositories(t *testing.T) {
	remote := newBare(t)
	a, b := newRepo(t), newRepo(t)
	c := commit(t, a, map[string][]byte{"issues/t-1.json": []byte("one")})

	// "" as the expected value: the remote ref must not exist yet.
	if err := a.Push(ctx, remote, c, syncRef, ""); err != nil {
		t.Fatal(err)
	}
	if got := run(t, remote, "rev-parse", syncRef); got != c {
		t.Fatalf("remote ref = %s, want %s", got, c)
	}

	found, err := b.Fetch(ctx, remote, syncRef, fetchedRef)
	if err != nil || !found {
		t.Fatalf("Fetch = %v, %v; want true, nil", found, err)
	}
	if got := run(t, b.Dir, "rev-parse", fetchedRef); got != c {
		t.Errorf("fetched ref = %s, want %s", got, c)
	}
	tree, err := b.ListTree(ctx, c)
	if err != nil || tree["issues/t-1.json"] != BlobHash([]byte("one")) {
		t.Errorf("the fetched commit's tree = %v, %v", tree, err)
	}
	if refs := run(t, b.Dir, "for-each-ref", "--format=%(refname)"); refs != fetchedRef {
		t.Errorf("fetch made other refs:\n%s", refs)
	}
}

func TestFetchWorksThroughARemoteName(t *testing.T) {
	remote := newBare(t)
	a, b := newRepo(t), newRepo(t)
	c := commit(t, a, map[string][]byte{"f": []byte("1")})
	run(t, a.Dir, "remote", "add", "origin", remote)
	run(t, b.Dir, "remote", "add", "origin", remote)
	if err := a.Push(ctx, "origin", c, syncRef, ""); err != nil {
		t.Fatal(err)
	}
	if found, err := b.Fetch(ctx, "origin", syncRef, fetchedRef); err != nil || !found {
		t.Fatalf("Fetch = %v, %v", found, err)
	}
}

func TestFetchOfARefTheRemoteDoesNotHaveIsNotAnError(t *testing.T) {
	remote := newBare(t)
	b := newRepo(t)
	found, err := b.Fetch(ctx, remote, syncRef, fetchedRef)
	if err != nil || found {
		t.Fatalf("Fetch = %v, %v; want false, nil", found, err)
	}
	if refs := run(t, b.Dir, "for-each-ref"); refs != "" {
		t.Errorf("refs after fetching nothing:\n%s", refs)
	}
}

func TestFetchFollowsARemoteRefThatWasRewritten(t *testing.T) {
	remote := newBare(t)
	a, b := newRepo(t), newRepo(t)
	one := commit(t, a, map[string][]byte{"f": []byte("1")})
	unrelated := commit(t, a, map[string][]byte{"f": []byte("2")})
	if err := a.Push(ctx, remote, one, syncRef, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Fetch(ctx, remote, syncRef, fetchedRef); err != nil {
		t.Fatal(err)
	}
	run(t, a.Dir, "push", "-q", "--force", remote, unrelated+":"+syncRef)
	if _, err := b.Fetch(ctx, remote, syncRef, fetchedRef); err != nil {
		t.Fatal(err)
	}
	if got := run(t, b.Dir, "rev-parse", fetchedRef); got != unrelated {
		t.Errorf("fetched ref = %s, want the remote's value %s", got, unrelated)
	}
}

func TestFetchFromAMissingPathIsRemoteNotFound(t *testing.T) {
	b := newRepo(t)
	nowhere := filepath.Join(t.TempDir(), "nowhere.git")
	_, err := b.Fetch(ctx, nowhere, syncRef, fetchedRef)
	te := wantCode(t, err, CodeRemoteNotFound)
	if !strings.Contains(te.Message, "does not appear to be a git repository") {
		t.Errorf("message doesn't carry git's reason: %s", te.Message)
	}
	if te.Hint == "" {
		t.Error("offline has no hint")
	}

	// The same through a configured remote whose URL leads nowhere.
	run(t, b.Dir, "remote", "add", "origin", nowhere)
	_, err = b.Fetch(ctx, "origin", syncRef, fetchedRef)
	wantCode(t, err, CodeRemoteNotFound)
}

func TestPushToAMissingPathIsRemoteNotFound(t *testing.T) {
	a := newRepo(t)
	c := commit(t, a, map[string][]byte{"f": []byte("1")})
	err := a.Push(ctx, filepath.Join(t.TempDir(), "nowhere.git"), c, syncRef, "")
	wantCode(t, err, CodeRemoteNotFound)
	if errors.Is(err, ErrRemoteMoved) {
		t.Error("an unreachable remote reads as a moved one")
	}
}

func TestPushAfterTheRemoteMovedIsErrRemoteMoved(t *testing.T) {
	remote := newBare(t)
	a, b := newRepo(t), newRepo(t)
	base := commit(t, a, map[string][]byte{"f": []byte("0")})
	if err := a.Push(ctx, remote, base, syncRef, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Fetch(ctx, remote, syncRef, fetchedRef); err != nil {
		t.Fatal(err)
	}

	// a pushes first; b then pushes a commit made on the value it fetched.
	fromA := commit(t, a, map[string][]byte{"f": []byte("a")}, base)
	if err := a.Push(ctx, remote, fromA, syncRef, base); err != nil {
		t.Fatal(err)
	}
	fromB := commit(t, b, map[string][]byte{"f": []byte("b")}, base)
	err := b.Push(ctx, remote, fromB, syncRef, base)
	if !errors.Is(err, ErrRemoteMoved) {
		t.Fatalf("error = %v, want ErrRemoteMoved", err)
	}
	if got := run(t, remote, "rev-parse", syncRef); got != fromA {
		t.Errorf("remote ref = %s, want it left at %s", got, fromA)
	}

	// A first push that finds the ref already there is the same case.
	c := newRepo(t)
	fromC := commit(t, c, map[string][]byte{"f": []byte("c")})
	if err := c.Push(ctx, remote, fromC, syncRef, ""); !errors.Is(err, ErrRemoteMoved) {
		t.Fatalf("first push onto an existing ref: %v, want ErrRemoteMoved", err)
	}

	// Fetching and pushing on top of what is there goes through.
	if _, err := b.Fetch(ctx, remote, syncRef, fetchedRef); err != nil {
		t.Fatal(err)
	}
	merged := commit(t, b, map[string][]byte{"f": []byte("ab")}, fromB, fromA)
	if err := b.Push(ctx, remote, merged, syncRef, fromA); err != nil {
		t.Fatal(err)
	}
	if got := run(t, remote, "rev-parse", syncRef); got != merged {
		t.Errorf("remote ref = %s, want %s", got, merged)
	}
}

func TestPushRejectedByAPreReceiveHookIsPushRejectedWithTheReason(t *testing.T) {
	remote := newBare(t)
	hook := "#!/bin/sh\necho 'refs/x is closed for the audit' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(remote, "hooks", "pre-receive"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	a := newRepo(t)
	c := commit(t, a, map[string][]byte{"f": []byte("1")})
	err := a.Push(ctx, remote, c, syncRef, "")
	te := wantCode(t, err, CodePushRejected)
	if errors.Is(err, ErrRemoteMoved) {
		t.Error("a policy rejection reads as retryable")
	}
	for _, want := range []string{"pre-receive hook declined", "refs/x is closed for the audit"} {
		if !strings.Contains(te.Message, want) {
			t.Errorf("message %q doesn't carry %q", te.Message, want)
		}
	}
	if out := run(t, remote, "for-each-ref"); out != "" {
		t.Errorf("a rejected push left refs on the remote:\n%s", out)
	}
}

func TestPushDoesNotRunTheRepositorysPrePushHook(t *testing.T) {
	remote := newBare(t)
	a := newRepo(t)
	hooks := filepath.Join(a.Dir, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "pre-push"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := commit(t, a, map[string][]byte{"f": []byte("1")})
	if err := a.Push(ctx, remote, c, syncRef, ""); err != nil {
		t.Fatalf("the user's pre-push hook stopped a sync push: %v", err)
	}
}

func TestACancelledContextIsNotReportedAsOffline(t *testing.T) {
	b := newRepo(t)
	done, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
	defer cancel()
	_, err := b.Fetch(done, newBare(t), syncRef, fetchedRef)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want the context's", err)
	}
}

func TestLsRemoteSaysWhetherTheRemoteHasTheRef(t *testing.T) {
	remote := newBare(t)
	a := newRepo(t)
	id, found, err := a.LsRemote(ctx, remote, syncRef)
	if err != nil || found || id != "" {
		t.Fatalf("empty remote: %q, %v, %v; want not found and no error", id, found, err)
	}
	c := commit(t, a, map[string][]byte{"issues/t-1.json": []byte("one")})
	if err := a.Push(ctx, remote, c, syncRef, ""); err != nil {
		t.Fatal(err)
	}
	id, found, err = a.LsRemote(ctx, remote, syncRef)
	if err != nil || !found || id != c {
		t.Errorf("after push: %q, %v, %v; want %s", id, found, err, c)
	}
	if refs := run(t, a.Dir, "for-each-ref", "--format=%(refname)"); strings.Contains(refs, "tess/remotes") {
		t.Errorf("ls-remote fetched something:\n%s", refs)
	}
}

func TestLsRemoteOfAMissingPathIsRemoteNotFound(t *testing.T) {
	a := newRepo(t)
	_, _, err := a.LsRemote(ctx, filepath.Join(t.TempDir(), "nowhere.git"), syncRef)
	wantCode(t, err, CodeRemoteNotFound)
}

func TestUnreachableIsClassifiedFromGitsReason(t *testing.T) {
	cases := []struct{ stderr, code string }{
		{"fatal: Authentication failed for 'https://github.com/x/y.git/'", CodeAuthFailed},
		{"git@github.com: Permission denied (publickey).\nfatal: Could not read from remote repository.", CodeAuthFailed},
		{"fatal: could not read Username for 'https://github.com': terminal prompts disabled", CodeAuthFailed},
		{"Host key verification failed.\nfatal: Could not read from remote repository.", CodeAuthFailed},
		{"ERROR: Repository not found.\nfatal: Could not read from remote repository.", CodeRemoteNotFound},
		{"fatal: repository 'https://github.com/x/y.git/' not found", CodeRemoteNotFound},
		{"fatal: '/tmp/nowhere.git' does not appear to be a git repository\nfatal: Could not read from remote repository.", CodeRemoteNotFound},
		{"fatal: unable to access 'https://github.com/x/y.git/': Could not resolve host: github.com", CodeHostUnresolved},
		{"ssh: Could not resolve hostname github.com: nodename nor servname provided, or not known", CodeHostUnresolved},
		{"fatal: unable to access 'http://127.0.0.1:1/x': Failed to connect to 127.0.0.1 port 1: Connection refused", CodeConnectionRefused},
		{"ssh: connect to host github.com port 22: Connection refused", CodeConnectionRefused},
		{"ssh: connect to host github.com port 22: Operation timed out", CodeConnectionTimedOut},
		{"fatal: unable to access 'https://x/': Failed to connect to x port 443 after 75003 ms: Connection timed out", CodeConnectionTimedOut},
		{"ssh: connect to host github.com port 22: Network is unreachable", CodeNetworkUnreachable},
		{"ssh: connect to host 10.0.0.9 port 22: No route to host", CodeNetworkUnreachable},
		{"fatal: something new git says", CodeOffline},
		{"", CodeOffline},
	}
	for _, c := range cases {
		te := wantCode(t, unreachable("origin", c.stderr), c.code)
		if te.Hint == "" || !strings.Contains(te.Message, "origin") {
			t.Errorf("%s: message %q hint %q", c.code, te.Message, te.Hint)
		}
	}
}

func TestFetchFromAClosedPortIsConnectionRefused(t *testing.T) {
	b := newRepo(t)
	_, err := b.Fetch(ctx, "http://127.0.0.1:1/nothing.git", syncRef, fetchedRef)
	wantCode(t, err, CodeConnectionRefused)
}
