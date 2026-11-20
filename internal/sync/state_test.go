package sync

import (
	"path/filepath"
	"testing"

	"github.com/plongitudes/tesserae/internal/store/sqlite"
	"github.com/plongitudes/tesserae/internal/tracker"
)

func (c *syncCopy) state() *State {
	c.t.Helper()
	state, err := ReadState(c.ctx, c.dir, c.db)
	if err != nil {
		c.t.Fatalf("ReadState: %v", err)
	}
	return state
}

func TestStateOfACopyThatNeverSynced(t *testing.T) {
	c := newSyncCopy(t, "")
	c.create("2026-01-01T00:00:01Z", tracker.NewIssue{Title: "One"})
	gone := c.create("2026-01-01T00:00:02Z", tracker.NewIssue{Title: "Mistake"})
	if _, err := c.tr.Retire(c.ctx, []string{gone}, ""); err != nil {
		t.Fatal(err)
	}
	got := c.state()
	if got.Schema != sqlite.SchemaVersion || got.Prefix != "t" || got.Issues != 1 {
		t.Errorf("schema %d, prefix %q, %d issues; want %d, t, 1 (a retired issue is not counted)",
			got.Schema, got.Prefix, got.Issues, sqlite.SchemaVersion)
	}
	if !got.LastAt.IsZero() || got.LastResult != "" || got.HasLKG {
		t.Errorf("last at %v, result %q, LKG %v; want none of them", got.LastAt, got.LastResult, got.HasLKG)
	}
	if got.Changed != 2 {
		t.Errorf("changed = %d, want 2: nothing has been pushed", got.Changed)
	}
}

func TestStateAfterASyncCountsTheIssuesChangedSince(t *testing.T) {
	c := newSyncCopy(t, newRemote(t))
	one := c.create("2026-01-01T00:00:01Z", tracker.NewIssue{Title: "One"})
	c.create("2026-01-01T00:00:02Z", tracker.NewIssue{Title: "Two"})
	c.sync("2026-01-01T00:01:00Z")

	got := c.state()
	if !got.LastAt.Equal(at("2026-01-01T00:01:00Z")) || got.LastResult != ResultOK || !got.HasLKG {
		t.Errorf("last at %v, result %q, LKG %v", got.LastAt, got.LastResult, got.HasLKG)
	}
	if got.Changed != 0 {
		t.Errorf("changed = %d right after a sync, want 0", got.Changed)
	}

	c.now = at("2026-01-02T00:00:00Z")
	if _, err := c.tr.Comment(c.ctx, one, "A comment is a change to its issue"); err != nil {
		t.Fatal(err)
	}
	c.create("2026-01-02T00:00:01Z", tracker.NewIssue{Title: "Three"})
	if got := c.state(); got.Changed != 2 || got.Issues != 3 {
		t.Errorf("changed = %d, issues = %d; want 2 and 3", got.Changed, got.Issues)
	}
}

func TestStateKeepsTheLastGoodTimeAndTheCodeOfAFailedSync(t *testing.T) {
	c := newSyncCopy(t, newRemote(t))
	c.sync("2026-01-01T00:01:00Z")
	gitRun(t, c.dir, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "nowhere.git"))
	c.now = at("2026-01-03T00:00:00Z")
	if _, err := Run(c.ctx, c.options()); err == nil {
		t.Fatal("sync with nowhere worked")
	}
	got := c.state()
	if !got.LastAt.Equal(at("2026-01-01T00:01:00Z")) || got.LastResult != CodeOffline {
		t.Errorf("last at %v, result %q", got.LastAt, got.LastResult)
	}
}

func TestStateWritesNothingAndNeedsADatabase(t *testing.T) {
	c := newSyncCopy(t, newRemote(t))
	c.create("2026-01-01T00:00:01Z", tracker.NewIssue{Title: "One"})
	c.sync("2026-01-01T00:01:00Z")
	before, ref := c.load(), refAt(t, c.dir, localRef)
	c.state()
	c.wantUntouched(before, ref)

	_, err := ReadState(c.ctx, c.dir, filepath.Join(t.TempDir(), "tess.db"))
	wantSyncCode(t, err, tracker.CodeNotInitialized)
}

func TestFindRemoteChoosesAsASyncDoesAndReachesOnlyWhenAsked(t *testing.T) {
	c := newSyncCopy(t, "")
	_, err := FindRemote(c.ctx, c.dir, "", false)
	wantSyncCode(t, err, CodeNoRemote)

	gitRun(t, c.dir, "remote", "add", "origin", filepath.Join(t.TempDir(), "nowhere.git"))
	if name, err := FindRemote(c.ctx, c.dir, "", false); err != nil || name != "origin" {
		t.Errorf("unreached: %q, %v; want origin and no error", name, err)
	}
	name, err := FindRemote(c.ctx, c.dir, "", true)
	wantSyncCode(t, err, CodeOffline)
	if name != "origin" {
		t.Errorf("an unreachable remote is still named: got %q", name)
	}

	// A remote with no issues on it yet is reachable.
	gitRun(t, c.dir, "remote", "set-url", "origin", newRemote(t))
	if name, err := FindRemote(c.ctx, c.dir, "", true); err != nil || name != "origin" {
		t.Errorf("reached: %q, %v", name, err)
	}
	if refAt(t, c.dir, localRef) != "" {
		t.Error("reaching the remote made a local ref")
	}
}
