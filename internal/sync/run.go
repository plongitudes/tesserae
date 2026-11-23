package sync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/plongitudes/tesserae/internal/git"
	"github.com/plongitudes/tesserae/internal/sync/gitref"
	"github.com/plongitudes/tesserae/internal/tracker"
)

// The codes a sync fails with (sync.md, "Command surface"), beside
// CodeConflict, CodePrefixMismatch, CodeFormatUnsupported and CodeBusy.
const (
	CodeNoRemote     = gitref.CodeNoRemote
	CodeOffline      = git.CodeOffline
	CodePushRejected = git.CodePushRejected
)

// pushAttempts is how many times a sync fetches, merges and pushes before
// a remote that keeps moving is push_rejected.
const pushAttempts = 3

// settings is what Run fixes and a test may not want to wait for.
type settings struct {
	lockWait time.Duration
	// fetched, if set, is called after each fetch: the moment at which
	// another copy's push makes this one's stale.
	fetched func()
}

// Run is the whole sync (sync.md, "What a sync is"): fetch, merge, push
// with retry, store, move the local ref. Nothing is written locally before
// the push succeeds, and a failed sync leaves the issues and the local ref
// as they were. Errors are *tracker.Error with the codes of sync.md.
func Run(ctx context.Context, o Options) (*Result, error) {
	return run(ctx, o, settings{lockWait: lockWait})
}

func run(ctx context.Context, o Options, s settings) (*Result, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if _, err := os.Stat(o.DBPath); errors.Is(err, fs.ErrNotExist) {
		return nil, tracker.NotInitialized()
	}
	result, err := locked(ctx, o, s)
	if err != nil && !o.DryRun && ctx.Err() == nil {
		// Whatever code the sync failed with, the choice of remote included,
		// so the hook and doctor can say why. Not busy: waiting on another
		// sync is not a sync that failed, and the other one writes ok.
		var refusal *tracker.Error
		if errors.As(err, &refusal) && refusal.Code != tracker.CodeNotInitialized && refusal.Code != CodeBusy {
			// Best effort: the sync has already failed for its own reason.
			_ = RecordFailure(ctx, o.DBPath, refusal.Code)
		}
	}
	return result, err
}

// locked chooses the remote, takes the lock and runs the exchange under it.
// The lock comes after the remote, so a sync with no remote makes no lock
// file.
func locked(ctx context.Context, o Options, s settings) (*Result, error) {
	transport, err := gitref.Open(ctx, git.Repo{Dir: o.Dir, NoPrompt: o.NoPrompt}, o.Remote)
	if err != nil {
		return nil, err
	}
	release, err := acquire(ctx, o.DBPath, s.lockWait)
	if err != nil {
		return nil, err
	}
	defer release()
	return exchange(ctx, o, s, transport)
}

// exchange is the sync under the lock: up to pushAttempts rounds of load,
// fetch, merge and push, then the two local writes.
func exchange(ctx context.Context, o Options, s settings, transport *gitref.Transport) (*Result, error) {
	at := o.Now()
	var again error
	for attempt := 0; attempt < pushAttempts; attempt++ {
		ours, err := Load(ctx, o.DBPath)
		if err != nil {
			return nil, err
		}
		lkgTree, err := transport.Local(ctx)
		if err != nil {
			return nil, err
		}
		theirsTree, err := transport.Fetch(ctx)
		if err != nil {
			return nil, err
		}
		if s.fetched != nil {
			s.fetched()
		}
		lkg, err := snapshotOf(lkgTree)
		if err != nil {
			return nil, fmt.Errorf("the last known good snapshot: %w", err)
		}
		theirs, err := snapshotOf(theirsTree)
		if err != nil {
			return nil, fmt.Errorf("the snapshot on %s: %w", transport.Remote(), err)
		}
		merged, resolved, err := Merge(lkg, ours, theirs)
		if err != nil {
			return nil, err
		}

		files := merged.Files()
		result := &Result{
			Remote:   transport.Remote(),
			Fetched:  issuesChanged(ours.Files(), files),
			Pushed:   issuesChanged(theirsTree.Files, files),
			Resolved: resolved,
			DryRun:   o.DryRun,
		}
		if o.DryRun {
			return result, nil
		}

		// 3. Push, unless the remote already holds exactly this.
		commit := theirsTree.Commit
		if !theirsTree.Present() || !sameFiles(theirsTree.Files, files) {
			commit, err = transport.Push(ctx, lkgTree, theirsTree, files, gitref.Commit{
				Actor: o.Actor, When: at, Message: "tess sync: " + result.Summary() + "\n",
			})
			if errors.Is(err, gitref.ErrRemoteMoved) {
				again = err
				continue
			}
			if err != nil {
				return nil, err
			}
		}
		// 4. Write the database; 5. then, and only then, move the ref.
		if err := Store(ctx, o.DBPath, ours, merged, at); errors.Is(err, ErrChangedDuringSync) {
			again = err
			continue
		} else if err != nil {
			return nil, err
		}
		if err := transport.Advance(ctx, lkgTree, commit); err != nil {
			return nil, err
		}
		return result, nil
	}
	if errors.Is(again, ErrChangedDuringSync) {
		return nil, &tracker.Error{
			Code:    CodeBusy,
			Message: fmt.Sprintf("issues kept changing while the sync ran (%v)", again),
			Hint:    "run `tess sync` again",
		}
	}
	return nil, &tracker.Error{
		Code:    CodePushRejected,
		Message: fmt.Sprintf("the remote %s moved %d times in a row while this sync tried to push", transport.Remote(), pushAttempts),
		Hint:    "other copies are syncing at the same time; run `tess sync` again",
	}
}

// snapshotOf parses the tree a ref points at; a ref that does not exist is
// no snapshot, which Merge reads as an empty one.
func snapshotOf(tree gitref.Tree) (*Snapshot, error) {
	if !tree.Present() {
		return nil, nil
	}
	return FromFiles(tree.Files)
}

// issuesChanged counts the issues with a record (the issue itself, a
// comment or an event) in to that from lacks or holds differently, or the
// other way round: how many issues moving from one tree to the other
// touches.
func issuesChanged(from, to map[string][]byte) int {
	changed := map[string]bool{}
	note := func(a, b map[string][]byte) {
		for path, data := range a {
			if other, ok := b[path]; ok && bytes.Equal(data, other) {
				continue
			}
			if id, ok := issueOfPath(path); ok {
				changed[id] = true
			}
		}
	}
	note(to, from)
	note(from, to)
	return len(changed)
}

// issuesChangedIDs is issuesChanged with the first tree given as blob IDs
// (a ref listed with ls-tree) and the second hashed with hash: the same
// count without reading a blob.
func issuesChangedIDs(from map[string]string, to map[string][]byte, hash func([]byte) string) int {
	changed := map[string]bool{}
	seen := map[string]bool{}
	for path, data := range to {
		seen[path] = true
		if id, ok := from[path]; ok && id == hash(data) {
			continue
		}
		if id, ok := issueOfPath(path); ok {
			changed[id] = true
		}
	}
	for path := range from {
		if seen[path] {
			continue
		}
		if id, ok := issueOfPath(path); ok {
			changed[id] = true
		}
	}
	return len(changed)
}

// issueOfPath is the issue a file of the tree belongs to. Texts and the
// config belong to none.
func issueOfPath(path string) (string, bool) {
	if id, ok := strings.CutPrefix(path, issuesDir); ok {
		return strings.TrimSuffix(id, jsonSuffix), true
	}
	for _, dir := range []string{commentsDir, eventsDir} {
		if rest, ok := strings.CutPrefix(path, dir); ok {
			id, _, _ := strings.Cut(rest, "/")
			return id, true
		}
	}
	return "", false
}

func sameFiles(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for path, data := range a {
		if other, ok := b[path]; !ok || !bytes.Equal(data, other) {
			return false
		}
	}
	return true
}

// Summary is the one line that says what a sync did (acceptance/README.md,
// "Sync output"): what `tess sync` prints and what the commit message
// carries.
func (r *Result) Summary() string {
	switch {
	case r.Fetched == 0 && r.Pushed == 0 && len(r.Resolved) == 0:
		return "up to date"
	case r.DryRun:
		line := fmt.Sprintf("would push %d", r.Pushed)
		if r.Fetched > 0 {
			line += fmt.Sprintf(", would fetch %d", r.Fetched)
		}
		return line
	}
	noun := "issues"
	if r.Fetched == 1 {
		noun = "issue"
	}
	line := fmt.Sprintf("fetched %d %s, pushed %d", r.Fetched, noun, r.Pushed)
	if n := len(r.Resolved); n > 0 {
		line += fmt.Sprintf(", resolved %d", n)
	}
	return line
}
