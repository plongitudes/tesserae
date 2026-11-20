package git

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestWriteCommitWritesTheTreeAndTakesIdentityFromItsArguments(t *testing.T) {
	// The environment names someone else, at another time; none of it may
	// reach the commit.
	t.Setenv("GIT_AUTHOR_NAME", "env author")
	t.Setenv("GIT_AUTHOR_EMAIL", "env@example.com")
	t.Setenv("GIT_AUTHOR_DATE", "2001-01-01T00:00:00Z")
	t.Setenv("GIT_COMMITTER_NAME", "env committer")
	t.Setenv("GIT_COMMITTER_EMAIL", "env@example.com")
	t.Setenv("GIT_COMMITTER_DATE", "2001-01-01T00:00:00Z")

	r := newRepo(t)
	hash, err := r.WriteCommit(ctx, Commit{
		Author:    alice,
		Committer: bob,
		Message:   "tess sync\n\nfetched 1 issue",
		Files: map[string][]byte{
			"config.json":         []byte("{\"format\":1}\n"),
			"issues/t-abc.json":   []byte("{}\n"),
			"texts/no-newline":    []byte("verbatim, no newline"),
			"texts/binary":        {0, 1, 2, '\n', 0xff},
			"events/t-abc/1.json": nil,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	got := run(t, r.Dir, "log", "-1", "--format=%an|%ae|%at|%cn|%ce|%ct|%P|%B", hash)
	want := fmt.Sprintf("alice|alice@example.com|%d|bob||%d||tess sync\n\nfetched 1 issue", alice.When.Unix(), bob.When.Unix())
	if got != want {
		t.Errorf("commit:\n got %q\nwant %q", got, want)
	}
	if got := run(t, r.Dir, "cat-file", "-p", hash+":texts/no-newline"); got != "verbatim, no newline" {
		t.Errorf("blob = %q", got)
	}
	if got := run(t, r.Dir, "ls-tree", "-r", "--name-only", hash); got != "config.json\nevents/t-abc/1.json\nissues/t-abc.json\ntexts/binary\ntexts/no-newline" {
		t.Errorf("tree:\n%s", got)
	}
	if refs := run(t, r.Dir, "for-each-ref"); refs != "" {
		t.Errorf("writing a commit moved or made a ref:\n%s", refs)
	}
}

func TestWriteCommitStartsFromItsFirstParentsTree(t *testing.T) {
	r := newRepo(t)
	one := commit(t, r, map[string][]byte{"keep": []byte("k"), "change": []byte("old"), "drop": []byte("d")})
	other := commit(t, r, map[string][]byte{"elsewhere": []byte("e")})
	two, err := r.WriteCommit(ctx, Commit{
		Parents: []string{one, other},
		Author:  alice, Committer: alice, Message: "merge",
		Files:  map[string][]byte{"change": []byte("new"), "add/deep": []byte("a")},
		Delete: []string{"drop"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := run(t, r.Dir, "log", "-1", "--format=%P", two); got != one+" "+other {
		t.Errorf("parents = %s, want %s %s", got, one, other)
	}
	tree, err := r.ListTree(ctx, two)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"keep": BlobHash([]byte("k")), "change": BlobHash([]byte("new")), "add/deep": BlobHash([]byte("a"))}
	if len(tree) != len(want) {
		t.Fatalf("tree = %v, want %v", tree, want)
	}
	for path, hash := range want {
		if tree[path] != hash {
			t.Errorf("tree[%s] = %s, want %s", path, tree[path], hash)
		}
	}
}

func TestWriteCommitIsDeterministic(t *testing.T) {
	c := Commit{Author: alice, Committer: bob, Message: "same", Files: map[string][]byte{"b": []byte("2"), "a": []byte("1"), "c/d": []byte("3")}}
	one, err := newRepo(t).WriteCommit(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	two, err := newRepo(t).WriteCommit(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if one != two {
		t.Errorf("the same commit written in two repositories: %s and %s", one, two)
	}
}

func TestWriteCommitRefusesAPathGitCannotHold(t *testing.T) {
	r := newRepo(t)
	for _, path := range []string{"", "a\nb", "/abs", "a//b", "a/../b", "trailing/"} {
		_, err := r.WriteCommit(ctx, Commit{Author: alice, Committer: alice, Message: "m", Files: map[string][]byte{path: []byte("x")}})
		if err == nil {
			t.Errorf("path %q: no error", path)
		}
	}
}

func TestBlobHashIsGitsHashOfTheBytes(t *testing.T) {
	// Known values: git hash-object of the empty blob and of "hello\n".
	if got := BlobHash(nil); got != "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391" {
		t.Errorf("empty blob = %s", got)
	}
	if got := BlobHash([]byte("hello\n")); got != "ce013625030ba8dba906f756967f9e9ca394464a" {
		t.Errorf("hello = %s", got)
	}
}

func TestListTreeGivesEveryFileOfACommitWithItsBlobHash(t *testing.T) {
	r := newRepo(t)
	files := map[string][]byte{
		"config.json":          []byte("{}\n"),
		"issues/t-1.json":      []byte("one"),
		"comments/t-1/c1.json": []byte("two"),
		"texts/with space":     []byte("three"),
	}
	tree, err := r.ListTree(ctx, commit(t, r, files))
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) != len(files) {
		t.Fatalf("tree = %v", tree)
	}
	for path, data := range files {
		if tree[path] != BlobHash(data) {
			t.Errorf("tree[%s] = %s, want %s", path, tree[path], BlobHash(data))
		}
	}
	if _, err := r.ListTree(ctx, strings.Repeat("1", 40)); err == nil {
		t.Error("listing a commit that isn't there: no error")
	}
}

func TestReadBlobsReadsManyBlobsInOrder(t *testing.T) {
	r := newRepo(t)
	files := map[string][]byte{}
	var want [][]byte
	var hashes []string
	// Enough data that the request and the answer can't both sit in a pipe
	// buffer: reading and writing have to run together.
	for i := 0; i < 3000; i++ {
		data := bytes.Repeat([]byte(fmt.Sprintf("blob %d\n", i)), 40)
		files[fmt.Sprintf("issues/t-%d.json", i)] = data
		want = append(want, data)
		hashes = append(hashes, BlobHash(data))
	}
	files["empty"] = nil
	files["binary"] = []byte{0, '\n', 0xff, '\n'}
	want = append(want, nil, files["binary"], want[0])
	hashes = append(hashes, BlobHash(nil), BlobHash(files["binary"]), hashes[0])
	commit(t, r, files)

	got, err := r.ReadBlobs(ctx, hashes)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("%d blobs, want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("blob %d = %q, want %q", i, got[i], want[i])
		}
	}

	if got, err := r.ReadBlobs(ctx, nil); err != nil || len(got) != 0 {
		t.Errorf("no blobs: %v, %v", got, err)
	}
	if _, err := r.ReadBlobs(ctx, []string{hashes[0], strings.Repeat("1", 40)}); err == nil {
		t.Error("reading a blob that isn't there: no error")
	}
}
