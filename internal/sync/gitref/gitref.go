// Package gitref is the git-ref transport of sync (ADR-0004, ADR-0012;
// docs/design/sync.md, "What a sync is" and "Git"). It is the only place
// that knows the ref names: the local ref, which is the last known good
// snapshot, the remote's ref, and the ref a fetch lands in. It deals in
// trees of files, path to bytes; what the files mean is internal/sync's
// business.
package gitref

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/plongitudes/tesserae/internal/git"
	"github.com/plongitudes/tesserae/internal/tracker"
)

const (
	// syncRef is the ref a snapshot lives on, here and on the remote. No
	// branch checks it out and a plain `git push` never carries it.
	syncRef = "refs/tess/sync"
	// originRemote is the remote used when none is named.
	originRemote = "origin"
)

// CodeNoRemote is for a sync with no remote to sync with, or several and no
// way to choose.
const CodeNoRemote = "no_remote"

// ErrRemoteMoved is Push's error when someone else pushed after the fetch.
// Fetching, merging and pushing again is the answer.
var ErrRemoteMoved = git.ErrRemoteMoved

// fetchedRef is where a fetch of the remote's ref lands.
func fetchedRef(remote string) string {
	return "refs/tess/remotes/" + remote + "/sync"
}

// Transport exchanges trees with one remote of one repository.
type Transport struct {
	repo   git.Repo
	remote string
	blobs  map[string][]byte // blob ID -> bytes, so a blob is read once
}

// Tree is the snapshot a ref points at: the commit and its files. The zero
// Tree is a ref that does not exist.
type Tree struct {
	Commit string
	Files  map[string][]byte // path -> bytes; treat as read-only

	ids map[string]string // path -> blob ID
}

// Present reports whether the ref exists.
func (t Tree) Present() bool { return t.Commit != "" }

// Open returns the transport for the repository dir is in and the remote
// named, or the default remote when name is empty: origin, else the only
// remote. The errors are *tracker.Error: not_a_git_repo, and CodeNoRemote
// when there is no such remote, none at all, or several and no origin.
func Open(ctx context.Context, dir, name string) (*Transport, error) {
	repo := git.Repo{Dir: dir}
	if _, err := repo.CommonDir(ctx); err != nil {
		return nil, err
	}
	remotes, err := repo.Remotes(ctx)
	if err != nil {
		return nil, err
	}
	remote, err := choose(remotes, name)
	if err != nil {
		return nil, err
	}
	return &Transport{repo: repo, remote: remote, blobs: map[string][]byte{}}, nil
}

// choose picks the remote (sync.md, "Git"). The name is never stored.
func choose(remotes []git.Remote, name string) (string, error) {
	names := make([]string, len(remotes))
	for i, r := range remotes {
		names[i] = r.Name
	}
	sort.Strings(names)
	has := func(want string) bool {
		i := sort.SearchStrings(names, want)
		return i < len(names) && names[i] == want
	}
	switch {
	case name != "" && has(name):
		return name, nil
	case name != "":
		return "", &tracker.Error{
			Code:    CodeNoRemote,
			Message: fmt.Sprintf("this repository has no remote named %s", name),
			Hint:    "`git remote` lists the remotes; `git remote add NAME URL` adds one",
		}
	case len(names) == 0:
		return "", &tracker.Error{
			Code:    CodeNoRemote,
			Message: "this repository has no remote to sync with",
			Hint:    "`git remote add origin URL`, then `tess sync`",
		}
	case has(originRemote):
		return originRemote, nil
	case len(names) == 1:
		return names[0], nil
	}
	return "", &tracker.Error{
		Code:    CodeNoRemote,
		Message: fmt.Sprintf("this repository has several remotes and none is origin: %s", strings.Join(names, ", ")),
		Hint:    "name one: `tess sync --remote NAME`",
	}
}

// Remote is the name of the remote this transport syncs with.
func (t *Transport) Remote() string { return t.remote }

// Local reads the local ref of the repository dir is in, with no remote in
// the matter: what the last sync left, for a report on this copy alone.
func Local(ctx context.Context, dir string) (Tree, error) {
	t := &Transport{repo: git.Repo{Dir: dir}, blobs: map[string][]byte{}}
	return t.Local(ctx)
}

// Local reads the local ref: the last known good snapshot.
func (t *Transport) Local(ctx context.Context) (Tree, error) {
	return t.read(ctx, syncRef)
}

// Fetch fetches the remote's ref and reads it. A remote that has no ref
// yet gives the zero Tree. A remote that can't be reached is offline.
func (t *Transport) Fetch(ctx context.Context) (Tree, error) {
	dst := fetchedRef(t.remote)
	found, err := t.repo.Fetch(ctx, t.remote, syncRef, dst)
	if err != nil || !found {
		// Without the ref on the remote, whatever an earlier fetch left
		// in dst is not the remote's state.
		return Tree{}, err
	}
	return t.read(ctx, dst)
}

// read resolves a ref and reads its commit's whole tree: one process for
// the ref, one for the listing, one for every blob not read before.
func (t *Transport) read(ctx context.Context, ref string) (Tree, error) {
	commit, ok, err := t.repo.ResolveRef(ctx, ref)
	if err != nil || !ok {
		return Tree{}, err
	}
	ids, err := t.repo.ListTree(ctx, commit)
	if err != nil {
		return Tree{}, err
	}
	var missing []string
	asked := map[string]bool{}
	for _, id := range ids {
		if _, have := t.blobs[id]; !have && !asked[id] {
			asked[id] = true
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	blobs, err := t.repo.ReadBlobs(ctx, missing)
	if err != nil {
		return Tree{}, err
	}
	for i, id := range missing {
		t.blobs[id] = blobs[i]
	}
	files := make(map[string][]byte, len(ids))
	for path, id := range ids {
		files[path] = t.blobs[id]
	}
	return Tree{Commit: commit, Files: files, ids: ids}, nil
}

// Commit is what Push writes besides the files.
type Commit struct {
	Actor   string    // author and committer
	When    time.Time // author and committer date
	Message string
}

// Push writes files as a commit whose parents are lkg and theirs (those
// that exist), and pushes it to the remote's ref on condition that the ref
// is still where the fetch found it, at theirs. It returns the commit.
// Only files that differ from the first parent's are written. Nothing
// local moves: not the local ref, not the fetched ref. The errors are
// ErrRemoteMoved, and *tracker.Error with push_rejected or offline.
func (t *Transport) Push(ctx context.Context, lkg, theirs Tree, files map[string][]byte, c Commit) (string, error) {
	var parents []string
	base := lkg
	if !lkg.Present() {
		base = theirs
	}
	for _, p := range []string{lkg.Commit, theirs.Commit} {
		if p != "" && (len(parents) == 0 || parents[0] != p) {
			parents = append(parents, p)
		}
	}
	changed := map[string][]byte{}
	for path, data := range files {
		if id, ok := base.ids[path]; !ok || id != git.BlobHash(data) {
			changed[path] = data
		}
	}
	var gone []string
	for path := range base.ids {
		if _, ok := files[path]; !ok {
			gone = append(gone, path)
		}
	}
	who := git.Signature{Name: c.Actor, When: c.When}
	commit, err := t.repo.WriteCommit(ctx, git.Commit{
		Parents: parents, Author: who, Committer: who, Message: c.Message,
		Files: changed, Delete: gone,
	})
	if err != nil {
		return "", err
	}
	if err := t.repo.Push(ctx, t.remote, commit, syncRef, theirs.Commit); err != nil {
		return "", err
	}
	return commit, nil
}

// Advance moves the local ref from lkg to commit: compare-and-swap, so a
// ref that something else moved meanwhile is left alone and the call
// fails (ADR-0012).
func (t *Transport) Advance(ctx context.Context, lkg Tree, commit string) error {
	if commit == lkg.Commit {
		return nil
	}
	if err := t.repo.UpdateRef(ctx, syncRef, commit, lkg.Commit); err != nil {
		return fmt.Errorf("the local sync ref moved while the sync ran: %w", err)
	}
	return nil
}
