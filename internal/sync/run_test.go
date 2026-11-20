package sync

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/plongitudes/tesserae/internal/tracker"
)

// --- helpers ----------------------------------------------------------------

const (
	localRef   = "refs/tess/sync"
	fetchedRef = "refs/tess/remotes/origin/sync"
)

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// refAt is the commit a ref points at, or "" when the ref is absent.
func refAt(t *testing.T, dir, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--verify", "--quiet", ref).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func newRemote(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "remote.git")
	gitRun(t, "", "init", "-q", "--bare", dir)
	return dir
}

// A syncCopy is a copy: a database driven through the tracker, and a
// repository whose origin is the remote.
type syncCopy struct {
	*testCopy
	dir string
}

func newSyncCopy(t *testing.T, remote string) *syncCopy {
	t.Helper()
	c := &syncCopy{testCopy: newCopy(t), dir: t.TempDir()}
	gitRun(t, c.dir, "init", "-q", ".")
	if remote != "" {
		gitRun(t, c.dir, "remote", "add", "origin", remote)
	}
	return c
}

func (c *syncCopy) options() Options {
	return Options{Dir: c.dir, DBPath: c.db, Now: func() time.Time { return c.now }, Actor: "tester"}
}

// sync runs a sync that must succeed, at the given time.
func (c *syncCopy) sync(when string) *Result {
	c.t.Helper()
	c.now = at(when)
	res, err := Run(c.ctx, c.options())
	if err != nil {
		c.t.Fatalf("sync: %v", err)
	}
	return res
}

// wantCounts checks what a sync says it moved.
func wantCounts(t *testing.T, res *Result, fetched, pushed int) {
	t.Helper()
	if res.Fetched != fetched || res.Pushed != pushed {
		t.Errorf("fetched %d, pushed %d; want %d, %d", res.Fetched, res.Pushed, fetched, pushed)
	}
}

func wantSyncCode(t *testing.T, err error, code string) *tracker.Error {
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

// wantUntouched checks that a failed sync left the copy as it was: the
// database's snapshot and the local ref.
func (c *syncCopy) wantUntouched(before *Snapshot, ref string) {
	c.t.Helper()
	if !sameFiles(before.Files(), c.load().Files()) {
		c.t.Errorf("the database changed")
	}
	if got := refAt(c.t, c.dir, localRef); got != ref {
		c.t.Errorf("local ref = %q, want it left at %q", got, ref)
	}
}

// twoCopies are a and b with one issue both have synced.
func twoCopies(t *testing.T) (remote string, a, b *syncCopy, id string) {
	t.Helper()
	remote = newRemote(t)
	a, b = newSyncCopy(t, remote), newSyncCopy(t, remote)
	id = a.create("2026-01-01T00:00:01Z", tracker.NewIssue{Title: "Shared"})
	a.sync("2026-01-01T00:01:00Z")
	b.sync("2026-01-01T00:02:00Z")
	return remote, a, b, id
}

// --- the sequence -------------------------------------------------------------

func TestRunPushesAndTheOtherCopyFetches(t *testing.T) {
	remote := newRemote(t)
	a, b := newSyncCopy(t, remote), newSyncCopy(t, remote)
	id := a.create("2026-01-01T00:00:01Z", tracker.NewIssue{Title: "Shared", Description: "Body\n"})

	res := a.sync("2026-01-01T00:01:00Z")
	wantCounts(t, res, 0, 1)
	if res.Remote != "origin" || res.DryRun || res.Resolved == nil || len(res.Resolved) != 0 {
		t.Errorf("result = %+v", res)
	}
	pushed := refAt(t, remote, localRef)
	if pushed == "" || refAt(t, a.dir, localRef) != pushed {
		t.Fatalf("remote ref = %q, a's local ref = %q; want the same commit", pushed, refAt(t, a.dir, localRef))
	}
	if got := gitRun(t, remote, "log", "-1", "--format=%an|%aI|%s", pushed); got != "tester|2026-01-01T00:01:00Z|tess sync: fetched 0 issues, pushed 1" {
		t.Errorf("commit = %s", got)
	}
	if got := a.config(ConfigLastAt) + " " + a.config(ConfigLastResult); got != "2026-01-01T00:01:00Z ok" {
		t.Errorf("last sync = %s", got)
	}

	res = b.sync("2026-01-01T00:02:00Z")
	wantCounts(t, res, 1, 0)
	if got := b.get(id); got.Title != "Shared" || got.Description != "Body\n" {
		t.Errorf("b's issue = %+v", got)
	}
	if !sameFiles(a.load().Files(), b.load().Files()) {
		t.Errorf("the copies differ after both synced")
	}
	// b had nothing the remote lacked: no commit was made, and b's LKG is
	// the remote's commit.
	if refAt(t, remote, localRef) != pushed || refAt(t, b.dir, localRef) != pushed {
		t.Errorf("remote ref = %s, b's local ref = %s; want both %s", refAt(t, remote, localRef), refAt(t, b.dir, localRef), pushed)
	}
}

func TestRunWithNothingNewIsUpToDateAndMakesNoCommit(t *testing.T) {
	remote, a, _, _ := twoCopies(t)
	before := refAt(t, remote, localRef)
	res := a.sync("2026-01-02T00:00:00Z")
	wantCounts(t, res, 0, 0)
	if res.Summary() != "up to date" {
		t.Errorf("summary = %q", res.Summary())
	}
	if refAt(t, remote, localRef) != before || refAt(t, a.dir, localRef) != before {
		t.Errorf("a sync with nothing to say moved a ref")
	}
}

func TestRunMergesBothCopiesEditsAndCommitsWithBothParents(t *testing.T) {
	remote, a, b, id := twoCopies(t)
	a.update("2026-01-02T00:00:00Z", id, tracker.Patch{Priority: ptr(0)})
	b.update("2026-01-02T00:00:01Z", id, tracker.Patch{Assignee: ptr("bob")})
	wantCounts(t, b.sync("2026-01-02T00:01:00Z"), 0, 1)
	lkg, theirs := refAt(t, a.dir, localRef), refAt(t, remote, localRef)

	res := a.sync("2026-01-02T00:02:00Z")
	wantCounts(t, res, 1, 1)
	if got := a.get(id); got.Priority != 0 || deref(got.Assignee) != "bob" {
		t.Errorf("a's issue: priority %d, assignee %v", got.Priority, deref(got.Assignee))
	}
	merged := refAt(t, remote, localRef)
	if got := gitRun(t, remote, "log", "-1", "--format=%P", merged); got != lkg+" "+theirs {
		t.Errorf("parents = %s, want the LKG %s and theirs %s", got, lkg, theirs)
	}
	if refAt(t, a.dir, localRef) != merged {
		t.Errorf("a's local ref is not the pushed commit")
	}
	wantCounts(t, b.sync("2026-01-02T00:03:00Z"), 1, 0)
	if !sameFiles(a.load().Files(), b.load().Files()) {
		t.Errorf("the copies differ after both synced")
	}
}

func TestRunReportsAResolution(t *testing.T) {
	_, a, b, id := twoCopies(t)
	a.update("2026-01-02T00:00:00Z", id, tracker.Patch{Title: ptr("From a")})
	b.update("2026-01-02T00:00:05Z", id, tracker.Patch{Title: ptr("From b")})
	b.sync("2026-01-02T00:01:00Z")
	res := a.sync("2026-01-02T00:02:00Z")
	want := Resolution{
		Issue: id, Field: "title", Kept: "From b", Dropped: "From a", Won: WonRemote, Reason: ReasonLater,
		KeptAt: at("2026-01-02T00:00:05Z"), DroppedAt: at("2026-01-02T00:00:00Z"),
	}
	if len(res.Resolved) != 1 || res.Resolved[0] != want {
		t.Errorf("resolved = %+v, want %+v", res.Resolved, want)
	}
	if res.Summary() != "fetched 1 issue, pushed 1, resolved 1" {
		t.Errorf("summary = %q", res.Summary())
	}
}

func TestRunCountsACommentAsAChangeToItsIssue(t *testing.T) {
	_, a, b, id := twoCopies(t)
	a.comment("2026-01-02T00:00:00Z", id, "a remark")
	wantCounts(t, a.sync("2026-01-02T00:01:00Z"), 0, 1)
	wantCounts(t, b.sync("2026-01-02T00:02:00Z"), 1, 0)
	if got := b.query(`SELECT text FROM comments`); len(got) != 1 {
		t.Errorf("b's comments = %v", got)
	}
}

func TestRunAfterLosingTheLocalRefMergesAgain(t *testing.T) {
	remote, a, b, id := twoCopies(t)
	b.update("2026-01-02T00:00:00Z", id, tracker.Patch{Title: ptr("From b")})
	b.sync("2026-01-02T00:01:00Z")
	gitRun(t, a.dir, "update-ref", "-d", localRef)
	a.update("2026-01-02T00:02:00Z", id, tracker.Patch{Priority: ptr(0)})
	a.sync("2026-01-02T00:03:00Z")
	if got := a.get(id); got.Title != "From b" || got.Priority != 0 {
		t.Errorf("a's issue = %q, priority %d", got.Title, got.Priority)
	}
	if refAt(t, a.dir, localRef) != refAt(t, remote, localRef) {
		t.Errorf("a's local ref is not the remote's")
	}
}

// --- dry run ------------------------------------------------------------------

func TestDryRunWritesNothingButTheFetchedRef(t *testing.T) {
	remote, a, b, id := twoCopies(t)
	b.create("2026-01-02T00:00:00Z", tracker.NewIssue{Title: "From b"})
	b.sync("2026-01-02T00:01:00Z")
	a.update("2026-01-02T00:02:00Z", id, tracker.Patch{Title: ptr("Edited on a")})
	a.create("2026-01-02T00:02:01Z", tracker.NewIssue{Title: "From a"})
	before, ref, remoteRef := a.load(), refAt(t, a.dir, localRef), refAt(t, remote, localRef)
	lastAt := a.config(ConfigLastAt)

	o := a.options()
	o.DryRun = true
	a.now = at("2026-01-02T00:03:00Z")
	res, err := Run(a.ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	wantCounts(t, res, 1, 2)
	if !res.DryRun || res.Summary() != "would push 2, would fetch 1" {
		t.Errorf("dry run = %v, summary %q", res.DryRun, res.Summary())
	}
	a.wantUntouched(before, ref)
	if got := refAt(t, remote, localRef); got != remoteRef {
		t.Errorf("a dry run pushed: remote ref = %s, was %s", got, remoteRef)
	}
	if got := refAt(t, a.dir, fetchedRef); got != remoteRef {
		t.Errorf("fetched ref = %s, want the remote's %s", got, remoteRef)
	}
	if got := a.config(ConfigLastAt); got != lastAt {
		t.Errorf("a dry run recorded a sync at %s", got)
	}
}

// --- the retry ----------------------------------------------------------------

func TestRunFetchesAndMergesAgainWhenTheRemoteMovedBeforeThePush(t *testing.T) {
	remote, a, b, id := twoCopies(t)
	a.update("2026-01-02T00:00:00Z", id, tracker.Patch{Title: ptr("From a")})
	b.update("2026-01-02T00:00:01Z", id, tracker.Patch{Priority: ptr(0)})

	// b's sync lands between a's first fetch and a's push.
	fetches := 0
	a.now = at("2026-01-02T00:02:00Z")
	res, err := run(a.ctx, a.options(), settings{lockWait: lockWait, fetched: func() {
		if fetches++; fetches == 1 {
			b.sync("2026-01-02T00:01:00Z")
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	if fetches != 2 {
		t.Errorf("fetched %d times, want 2", fetches)
	}
	wantCounts(t, res, 1, 1)
	if got := a.get(id); got.Title != "From a" || got.Priority != 0 {
		t.Errorf("a's issue = %q, priority %d; want both edits", got.Title, got.Priority)
	}
	if refAt(t, a.dir, localRef) != refAt(t, remote, localRef) {
		t.Errorf("a's local ref is not the remote's")
	}
	wantCounts(t, b.sync("2026-01-02T00:03:00Z"), 1, 0)
	if got := b.get(id); got.Title != "From a" || got.Priority != 0 {
		t.Errorf("b's issue = %q, priority %d; b's push was overwritten", got.Title, got.Priority)
	}
}

func TestRunGivesUpAfterTheRemoteMovedThreeTimes(t *testing.T) {
	remote, a, b, id := twoCopies(t)
	a.update("2026-01-02T00:00:00Z", id, tracker.Patch{Title: ptr("From a")})
	before, ref := a.load(), refAt(t, a.dir, localRef)

	fetches := 0
	a.now = at("2026-01-02T01:00:00Z")
	_, err := run(a.ctx, a.options(), settings{lockWait: lockWait, fetched: func() {
		fetches++
		b.update("2026-01-02T00:00:01Z", id, tracker.Patch{Priority: ptr(fetches)})
		b.sync("2026-01-02T00:01:00Z")
	}})
	wantSyncCode(t, err, CodePushRejected)
	if fetches != 3 {
		t.Errorf("tried %d times, want 3", fetches)
	}
	a.wantUntouched(before, ref)
	if refAt(t, remote, localRef) != refAt(t, b.dir, localRef) {
		t.Errorf("the remote does not hold b's last push")
	}
	if got := a.config(ConfigLastAt) + " " + a.config(ConfigLastResult); got != "2026-01-01T00:01:00Z push_rejected" {
		t.Errorf("last sync = %s; want the time of the last good one and the code", got)
	}
}

func TestRunMergesAgainWhenACommandChangedAnIssueDuringTheSync(t *testing.T) {
	remote, a, b, id := twoCopies(t)
	b.update("2026-01-02T00:00:00Z", id, tracker.Patch{Title: ptr("From b")})
	b.sync("2026-01-02T00:01:00Z")

	// While a's sync is out fetching, a command on a edits the issue the
	// merge is about to change.
	fetches := 0
	a.now = at("2026-01-02T00:02:00Z")
	res, err := run(a.ctx, a.options(), settings{lockWait: lockWait, fetched: func() {
		if fetches++; fetches == 1 {
			a.update("2026-01-02T00:02:00Z", id, tracker.Patch{Priority: ptr(0)})
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	wantCounts(t, res, 1, 1)
	if got := a.get(id); got.Title != "From b" || got.Priority != 0 {
		t.Errorf("a's issue = %q, priority %d; want both edits", got.Title, got.Priority)
	}
	if refAt(t, a.dir, localRef) != refAt(t, remote, localRef) {
		t.Errorf("a's local ref is not the remote's")
	}
}

// The push went through and the database write did not: the local ref stays
// at the LKG (sync.md, "The order of 4 and 5 matters"), so the next sync
// merges the same inputs again.
func TestAPushedSyncWhoseDatabaseWriteFailsLeavesTheLocalRefAndConvergesNextTime(t *testing.T) {
	remote, a, b, id := twoCopies(t)
	b.update("2026-01-02T00:00:00Z", id, tracker.Patch{Title: ptr("From b")})
	b.sync("2026-01-02T00:01:00Z")
	ref, remoteRef := refAt(t, a.dir, localRef), refAt(t, remote, localRef)

	// A command on a edits the issue during every attempt.
	fetches := 0
	a.now = at("2026-01-02T00:02:00Z")
	_, err := run(a.ctx, a.options(), settings{lockWait: lockWait, fetched: func() {
		fetches++
		a.update("2026-01-02T00:02:00Z", id, tracker.Patch{Priority: ptr(fetches)})
	}})
	wantSyncCode(t, err, CodeBusy)
	if fetches != 3 {
		t.Errorf("tried %d times, want 3", fetches)
	}
	if refAt(t, remote, localRef) == remoteRef {
		t.Fatalf("the remote did not move: the push is not what failed here")
	}
	if got := refAt(t, a.dir, localRef); got != ref {
		t.Errorf("local ref = %s, want it left at the LKG %s", got, ref)
	}
	if got := a.get(id).Title; got != "Shared" {
		t.Errorf("a's title = %q: the database was written", got)
	}

	a.sync("2026-01-02T00:03:00Z")
	if got := a.get(id); got.Title != "From b" || got.Priority != 3 {
		t.Errorf("a's issue = %q, priority %d; want b's title and a's last priority", got.Title, got.Priority)
	}
	if refAt(t, a.dir, localRef) != refAt(t, remote, localRef) {
		t.Errorf("a's local ref is not the remote's")
	}
	wantCounts(t, b.sync("2026-01-02T00:04:00Z"), 1, 0)
	if got := b.get(id); got.Title != "From b" || got.Priority != 3 {
		t.Errorf("b's issue = %q, priority %d", got.Title, got.Priority)
	}
}

// --- failures leave the copy as it was ----------------------------------------

func TestAFailedPushLeavesTheDatabaseAndTheLocalRefUntouched(t *testing.T) {
	remote, a, b, id := twoCopies(t)
	b.update("2026-01-02T00:00:00Z", id, tracker.Patch{Title: ptr("From b")})
	b.sync("2026-01-02T00:01:00Z")
	a.update("2026-01-02T00:02:00Z", id, tracker.Patch{Priority: ptr(0)})
	before, ref, remoteRef := a.load(), refAt(t, a.dir, localRef), refAt(t, remote, localRef)

	hook := filepath.Join(remote, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho 'refused by policy' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	a.now = at("2026-01-02T00:03:00Z")
	_, err := Run(a.ctx, a.options())
	if te := wantSyncCode(t, err, CodePushRejected); !strings.Contains(te.Message, "refused by policy") {
		t.Errorf("message = %q, want the remote's reason", te.Message)
	}
	// Not even what the fetch brought is written: b's title is not here.
	a.wantUntouched(before, ref)
	if got := a.get(id).Title; got != "Shared" {
		t.Errorf("a's title = %q after a failed sync", got)
	}
	if got := refAt(t, remote, localRef); got != remoteRef {
		t.Errorf("remote ref = %s, was %s", got, remoteRef)
	}
	if got := a.config(ConfigLastResult); got != CodePushRejected {
		t.Errorf("last result = %s", got)
	}

	// With the refusal gone, the same sync goes through.
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	wantCounts(t, a.sync("2026-01-02T00:04:00Z"), 1, 1)
	if got := a.get(id); got.Title != "From b" || got.Priority != 0 {
		t.Errorf("a's issue = %q, priority %d", got.Title, got.Priority)
	}
}

func TestRunAgainstAnUnreachableRemoteIsOffline(t *testing.T) {
	_, a, _, id := twoCopies(t)
	a.update("2026-01-02T00:00:00Z", id, tracker.Patch{Priority: ptr(0)})
	before, ref := a.load(), refAt(t, a.dir, localRef)
	gitRun(t, a.dir, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "nowhere.git"))
	_, err := Run(a.ctx, a.options())
	wantSyncCode(t, err, CodeOffline)
	a.wantUntouched(before, ref)
}

func TestRunRefusesAnotherTrackersRemote(t *testing.T) {
	remote, _, _, _ := twoCopies(t)
	c := &syncCopy{testCopy: &testCopy{t: t, ctx: context.Background(), db: filepath.Join(t.TempDir(), "tess.db"), now: at("2026-01-03T00:00:00Z")}, dir: t.TempDir()}
	if err := tracker.Init(c.ctx, c.db, "u"); err != nil {
		t.Fatal(err)
	}
	gitRun(t, c.dir, "init", "-q", ".")
	gitRun(t, c.dir, "remote", "add", "origin", remote)
	before := refAt(t, remote, localRef)
	_, err := Run(c.ctx, c.options())
	wantSyncCode(t, err, CodePrefixMismatch)
	if refAt(t, remote, localRef) != before || refAt(t, c.dir, localRef) != "" {
		t.Errorf("a refused sync moved a ref")
	}
}

// --- the remote ---------------------------------------------------------------

func TestRunWithoutARemoteIsNoRemote(t *testing.T) {
	a := newSyncCopy(t, "")
	a.create("2026-01-01T00:00:01Z", tracker.NewIssue{Title: "Local"})
	// A dry run writes nothing, its failure included.
	o := a.options()
	o.DryRun = true
	_, err := Run(a.ctx, o)
	wantSyncCode(t, err, CodeNoRemote)
	if got := a.config(ConfigLastResult); got != "<unset>" {
		t.Errorf("sync.last_result = %s after a dry run", got)
	}
	// A sync's is recorded like any other code a sync fails with (sync.md,
	// "Command surface"), so doctor can say why.
	_, err = Run(a.ctx, a.options())
	wantSyncCode(t, err, CodeNoRemote)
	if got := a.config(ConfigLastResult); got != CodeNoRemote {
		t.Errorf("sync.last_result = %s, want %s", got, CodeNoRemote)
	}

	// Several remotes and no origin: no_remote until one is named.
	one, two := newRemote(t), newRemote(t)
	gitRun(t, a.dir, "remote", "add", "one", one)
	gitRun(t, a.dir, "remote", "add", "two", two)
	_, err = Run(a.ctx, a.options())
	wantSyncCode(t, err, CodeNoRemote)

	o = a.options()
	o.Remote = "two"
	res, err := Run(a.ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Remote != "two" || refAt(t, two, localRef) == "" || refAt(t, one, localRef) != "" {
		t.Errorf("synced with %q; refs: one %q, two %q", res.Remote, refAt(t, one, localRef), refAt(t, two, localRef))
	}
	if got := refAt(t, a.dir, "refs/tess/remotes/two/sync"); got != "" {
		t.Errorf("a push wrote the fetched ref: %s", got)
	}
}

func TestRunWithoutADatabaseIsNotInitialized(t *testing.T) {
	a := newSyncCopy(t, newRemote(t))
	o := a.options()
	o.DBPath = filepath.Join(t.TempDir(), "tess.db")
	_, err := Run(a.ctx, o)
	wantSyncCode(t, err, tracker.CodeNotInitialized)
	if _, statErr := os.Stat(o.DBPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("a refused sync made a database")
	}
	if _, statErr := os.Stat(lockPath(o.DBPath)); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("a refused sync made a lock file")
	}
}

// --- the lock -----------------------------------------------------------------

func TestRunIsBusyWhileAnotherSyncHoldsTheLock(t *testing.T) {
	remote := newRemote(t)
	a := newSyncCopy(t, remote)
	a.create("2026-01-01T00:00:01Z", tracker.NewIssue{Title: "Shared"})
	before := a.load()

	release, err := acquire(a.ctx, a.db, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = run(a.ctx, a.options(), settings{lockWait: 200 * time.Millisecond})
	wantSyncCode(t, err, CodeBusy)
	if waited := time.Since(start); waited < 200*time.Millisecond {
		t.Errorf("gave up after %s, before the wait was over", waited)
	}
	a.wantUntouched(before, "")
	if refAt(t, remote, localRef) != "" || refAt(t, a.dir, fetchedRef) != "" {
		t.Errorf("a sync that never got the lock touched a ref")
	}
	if got := a.config(ConfigLastResult); got != CodeBusy {
		t.Errorf("sync.last_result = %s, want %s", got, CodeBusy)
	}

	release()
	wantCounts(t, a.sync("2026-01-01T00:01:00Z"), 0, 1)
}

func TestRunWaitsForAnotherSyncToFinish(t *testing.T) {
	a := newSyncCopy(t, newRemote(t))
	a.create("2026-01-01T00:00:01Z", tracker.NewIssue{Title: "Shared"})
	release, err := acquire(a.ctx, a.db, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan time.Time, 1)
	go func() {
		time.Sleep(150 * time.Millisecond)
		released <- time.Now()
		release()
	}()
	res, err := run(a.ctx, a.options(), settings{lockWait: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if time.Now().Before(<-released) {
		t.Errorf("the sync ran while the lock was held")
	}
	wantCounts(t, res, 0, 1)
}

func TestTheLockIsReleasedWhenASyncFails(t *testing.T) {
	a := newSyncCopy(t, filepath.Join(t.TempDir(), "nowhere.git"))
	for i := 0; i < 2; i++ {
		_, err := run(a.ctx, a.options(), settings{lockWait: 100 * time.Millisecond})
		wantSyncCode(t, err, CodeOffline)
	}
}

func TestWaitingForTheLockEndsWithTheContext(t *testing.T) {
	a := newSyncCopy(t, newRemote(t))
	release, err := acquire(a.ctx, a.db, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(a.ctx, 100*time.Millisecond)
	defer cancel()
	_, err = run(ctx, a.options(), settings{lockWait: 30 * time.Second})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want the context's deadline", err)
	}
	var te *tracker.Error
	if errors.As(err, &te) {
		t.Errorf("a deadline was reported as %s", te.Code)
	}
}

// --- the summary ----------------------------------------------------------------

func TestSummaryIsTheOneLineOfTheReadme(t *testing.T) {
	one := []Resolution{{}}
	for _, c := range []struct {
		res  Result
		want string
	}{
		{Result{}, "up to date"},
		{Result{DryRun: true}, "up to date"},
		{Result{Fetched: 12, Pushed: 3, Resolved: one}, "fetched 12 issues, pushed 3, resolved 1"},
		{Result{Fetched: 2, Pushed: 1}, "fetched 2 issues, pushed 1"},
		{Result{Pushed: 1}, "fetched 0 issues, pushed 1"},
		{Result{Fetched: 1}, "fetched 1 issue, pushed 0"},
		{Result{Pushed: 3, DryRun: true}, "would push 3"},
		{Result{Fetched: 2, Pushed: 3, DryRun: true}, "would push 3, would fetch 2"},
		{Result{Fetched: 2, DryRun: true}, "would push 0, would fetch 2"},
	} {
		if got := c.res.Summary(); got != c.want {
			t.Errorf("%+v: summary = %q, want %q", c.res, got, c.want)
		}
	}
}
