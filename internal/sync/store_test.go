package sync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/plongitudes/tesserae/internal/store/sqlite"
	"github.com/plongitudes/tesserae/internal/tracker"
)

// --- helpers ----------------------------------------------------------------

var syncTime = at("2026-02-01T12:00:00Z")

// store writes a merged snapshot into the copy's database, ours being the
// snapshot the merge was given.
func (c *testCopy) store(ours, merged *Snapshot) {
	c.t.Helper()
	if err := Store(c.ctx, c.db, ours, merged, syncTime); err != nil {
		c.t.Fatalf("Store: %v", err)
	}
}

// query reads rows straight from the copy's database, each row as its
// columns joined with "|", NULL as "<null>".
func (c *testCopy) query(query string, args ...any) []string {
	c.t.Helper()
	db, err := sqlite.Open(c.ctx, c.db)
	if err != nil {
		c.t.Fatal(err)
	}
	defer db.Close()
	var out []string
	err = db.Read(c.ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(c.ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			return err
		}
		for rows.Next() {
			vals := make([]sql.NullString, len(cols))
			dest := make([]any, len(cols))
			for i := range vals {
				dest[i] = &vals[i]
			}
			if err := rows.Scan(dest...); err != nil {
				return err
			}
			row := make([]string, len(cols))
			for i, v := range vals {
				row[i] = "<null>"
				if v.Valid {
					row[i] = v.String
				}
			}
			out = append(out, strings.Join(row, "|"))
		}
		return rows.Err()
	})
	if err != nil {
		c.t.Fatalf("%s: %v", query, err)
	}
	return out
}

func (c *testCopy) config(key string) string {
	c.t.Helper()
	rows := c.query(`SELECT value FROM config WHERE key = ?`, key)
	if len(rows) == 0 {
		return "<unset>"
	}
	return rows[0]
}

func (c *testCopy) status(when, id, status, reason string) {
	c.t.Helper()
	c.now = at(when)
	if _, err := c.tr.Status(c.ctx, []string{id}, status, reason, false); err != nil {
		c.t.Fatalf("status %s %s: %v", id, status, err)
	}
}

func (c *testCopy) comment(when, id, text string) {
	c.t.Helper()
	c.now = at(when)
	if _, err := c.tr.Comment(c.ctx, id, text); err != nil {
		c.t.Fatalf("comment on %s: %v", id, err)
	}
}

func (c *testCopy) listed(f tracker.Filter) []string {
	c.t.Helper()
	issues, err := c.tr.List(c.ctx, f)
	if err != nil {
		c.t.Fatalf("list: %v", err)
	}
	ids := []string{}
	for _, i := range issues {
		ids = append(ids, i.ID)
	}
	slices.Sort(ids)
	return ids
}

// wantLoads checks that the database now reads as the snapshot.
func (c *testCopy) wantLoads(want *Snapshot) {
	c.t.Helper()
	wantSameFiles(c.t, c.load().Files(), want.Files())
}

// addText puts a body in the snapshot and returns its hash.
func addText(s *Snapshot, body string) string {
	h := hashOf(body)
	s.Texts[h] = body
	return h
}

func addComment(s *Snapshot, copy, id string, n int, text string) {
	c := &Comment{ID: fmt.Sprintf("cm%04d%s", n, copy), IssueID: id, Author: copy, Text: text, CreatedAt: day(n)}
	s.Comments[c.ID] = c
}

// --- Load after the write equals the merged snapshot ------------------------

// One copy built through the tracker, and a second that changed every kind
// of thing a snapshot holds.
func TestStoreWritesEveryKindOfChange(t *testing.T) {
	c := newCopy(t)
	epic := c.create("2026-01-01T00:00:01Z", tracker.NewIssue{Title: "Epic", Type: "epic"})
	blocker := c.create("2026-01-01T00:00:02Z", tracker.NewIssue{Title: "Blocker"})
	work := c.create("2026-01-01T00:00:03Z", tracker.NewIssue{
		Title: "Work", Description: "What to do", Labels: []string{"keep", "drop"},
		Parent: epic, BlockedBy: []string{blocker}, Assignee: "alice",
	})
	origin := c.create("2026-01-01T00:00:04Z", tracker.NewIssue{Title: "Origin"})
	c.link("2026-01-01T00:00:05Z", work, "discovered-from", origin)
	// Given from the larger ID, so the row is stored the other way round
	// from the snapshot, which holds the link on the smaller.
	small, large := min(origin, epic), max(origin, epic)
	c.link("2026-01-01T00:00:06Z", large, "related", small)
	c.comment("2026-01-01T00:00:07Z", work, "ours")
	ours := c.load()

	theirs := fork(t, ours)
	const b = "b"
	setTitle(theirs, b, work, 100, "Work <renamed> & more")
	edit(theirs, b, work, 101, "description", ours.Issues[work].Description, addText(theirs, "Second draft\n"),
		func(i *Issue) { i.Description = hashOf("Second draft\n") })
	edit(theirs, b, work, 102, "priority", float64(2), float64(0), func(i *Issue) { i.Priority = 0 })
	edit(theirs, b, work, 103, "assignee", "alice", nil, func(i *Issue) { i.Assignee = nil })
	edit(theirs, b, work, 104, "owner", nil, "bob", func(i *Issue) { i.Owner = ptr("bob") })
	edit(theirs, b, work, 105, "due_at", nil, "2026-06-01T00:00:00Z", func(i *Issue) { i.DueAt = ptr(at("2026-06-01T00:00:00Z")) })
	edit(theirs, b, work, 106, "status", "ready", "held", func(i *Issue) { i.Status = "held" })
	logEvent(theirs, b, work, 106, "status_reason", nil, "waiting on a decision")
	theirs.Issues[work].StatusReason = ptr("waiting on a decision")
	addTo(theirs, b, work, 107, "labels", "new")
	removeFrom(theirs, b, work, 108, "labels", "drop")
	removeFrom(theirs, b, work, 109, "blocked_by", blocker)
	removeFrom(theirs, b, work, 110, "discovered_from", origin)
	edit(theirs, b, work, 111, "parent", epic, nil, func(i *Issue) { i.Parent = nil })
	removeFrom(theirs, b, small, 112, "related", large)
	retire(theirs, b, origin, 113)
	addComment(theirs, b, work, 114, "theirs")
	// A new issue with a text, a label and every kind of link.
	fresh := base("t-new")
	fresh.Notes = addText(theirs, "notes of the new one")
	fresh.Labels = []string{"x"}
	fresh.Parent = ptr(epic)
	fresh.BlockedBy = []string{blocker}
	fresh.DiscoveredFrom = []string{work}
	fresh.Related = []string{"t-zzz"}
	theirs.Issues[fresh.ID] = fresh
	theirs.Issues["t-zzz"] = base("t-zzz")
	logEvent(theirs, b, fresh.ID, 115, "labels", nil, "x")

	merged, _ := merge(t, ours, ours, theirs)
	wantSameFiles(t, merged.Files(), theirs.Files())
	c.store(ours, merged)
	c.wantLoads(merged)

	// The tracker reads the written rows as any others.
	got := c.get(work)
	if got.Title != "Work <renamed> & more" || got.Description != "Second draft\n" || got.Status != "held" ||
		!reflect.DeepEqual(got.Labels, []string{"keep", "new"}) || got.Parent != nil || len(got.Comments) != 2 {
		t.Errorf("the tracker shows %+v", got)
	}
	if got := c.get(fresh.ID); got.Notes != "notes of the new one" || deref(got.Parent) != epic {
		t.Errorf("the tracker shows the new issue as %+v", got)
	}
}

// Two copies built through the tracker, merged, and each written with the
// same merged snapshot: both then read as it.
func TestStoreOnBothCopiesGivesTheSameSnapshot(t *testing.T) {
	a, b := newCopy(t), newCopy(t)
	var shared [2]string
	for i, c := range []*testCopy{a, b} {
		shared[i] = c.create("2026-01-01T00:00:01Z", tracker.NewIssue{Title: "Shared"})
	}
	if shared[0] != shared[1] {
		t.Fatalf("the shared issue is %s on one copy and %s on the other", shared[0], shared[1])
	}
	onA := a.create("2026-01-02T00:00:00.250Z", tracker.NewIssue{Title: "Made on a", Description: "a's text", Labels: []string{"a"}})
	a.link("2026-01-02T00:00:01Z", shared[0], "blocked-by", onA)
	a.comment("2026-01-02T00:00:02Z", shared[0], "from a")
	onB := b.create("2026-01-03T00:00:00.750Z", tracker.NewIssue{Title: "Made on b", Parent: shared[0]})
	b.update("2026-01-03T00:00:01Z", shared[0], tracker.Patch{Title: ptr("Shared, renamed on b"), AddLabels: []string{"b"}})
	b.status("2026-01-03T00:00:02Z", onB, "done", "")

	oursA, oursB := a.load(), b.load()
	merged, res := merge(t, nil, oursA, oursB)
	if len(res) != 0 {
		t.Fatalf("resolutions %+v", res)
	}
	a.store(oursA, merged)
	b.store(oursB, merged)
	a.wantLoads(merged)
	b.wantLoads(merged)

	// Each copy can go on working on what the other made.
	b.update("2026-01-04T00:00:00Z", onA, tracker.Patch{Title: ptr("Made on a, renamed on b")})
	a.status("2026-01-04T00:00:01Z", onB, "ready", "")
}

// The write logs nothing: the events in the database afterwards are the
// merged snapshot's, and no issue's updated_at is the time of the sync.
func TestStoreLogsNoEvents(t *testing.T) {
	c := newCopy(t)
	x := c.create("2026-01-01T00:00:01Z", tracker.NewIssue{Title: "X"})
	ours := c.load()
	theirs := fork(t, ours)
	setTitle(theirs, "b", x, 100, "X, renamed")
	addTo(theirs, "b", x, 101, "labels", "l")
	merged, _ := merge(t, ours, ours, theirs)

	c.store(ours, merged)
	if got := len(c.query(`SELECT id FROM events`)); got != len(merged.Events) {
		t.Errorf("%d events in the database, the merged snapshot has %d", got, len(merged.Events))
	}
	if got := c.query(`SELECT updated_at FROM issues WHERE id = ?`, x); got[0] != "2026-01-01T00:01:41.000Z" {
		t.Errorf("updated_at = %s, want the merged snapshot's", got[0])
	}
}

// Writing the same merged snapshot again (the sync after a failed move of
// the local ref) changes nothing.
func TestStoreTwiceIsTheSameAsOnce(t *testing.T) {
	c := newCopy(t)
	x := c.create("2026-01-01T00:00:01Z", tracker.NewIssue{Title: "X"})
	ours := c.load()
	theirs := fork(t, ours)
	setTitle(theirs, "b", x, 100, "X, renamed")
	addComment(theirs, "b", x, 101, "hello")
	merged, _ := merge(t, ours, ours, theirs)

	c.store(ours, merged)
	c.store(c.load(), merged)
	c.wantLoads(merged)
}

// --- what the write leaves alone --------------------------------------------

const wholeRow = `SELECT * FROM issues WHERE id = ?`

func TestStoreLeavesAnUnchangedIssuesRowUntouched(t *testing.T) {
	c := newCopy(t)
	// Milliseconds are in the database and not in a snapshot, so a rewritten
	// row would show.
	same := c.create("2026-01-01T00:00:01.250Z", tracker.NewIssue{Title: "Same", Labels: []string{"l"}})
	changed := c.create("2026-01-01T00:00:02.500Z", tracker.NewIssue{Title: "Changed", BlockedBy: []string{same}})
	c.update("2026-01-01T00:00:03.750Z", same, tracker.Patch{DueAt: ptr(at("2026-06-01T00:00:00.125Z"))})
	before := c.query(wholeRow, same)
	links := c.query(`SELECT * FROM links`)
	ours := c.load()

	theirs := fork(t, ours)
	setTitle(theirs, "b", changed, 100, "Changed, renamed")
	merged, _ := merge(t, ours, ours, theirs)
	c.store(ours, merged)

	if after := c.query(wholeRow, same); !reflect.DeepEqual(after, before) {
		t.Errorf("the unchanged issue's row was rewritten:\n%v\n%v", before, after)
	}
	if after := c.query(`SELECT * FROM links`); !reflect.DeepEqual(after, links) {
		t.Errorf("the links were rewritten:\n%v\n%v", links, after)
	}
	// The changed row keeps the milliseconds of what the merge did not move.
	row := c.query(`SELECT title, created_at, updated_at FROM issues WHERE id = ?`, changed)
	if want := "Changed, renamed|2026-01-01T00:00:02.500Z|2026-01-01T00:01:40.000Z"; row[0] != want {
		t.Errorf("the changed row is %s, want %s", row[0], want)
	}
}

// A changed row keeps the milliseconds of every timestamp the merge did not
// move: the other copy's edit is the earlier one, so updated_at stays ours.
func TestStoreKeepsTheMillisecondsOfTimestampsItDidNotMove(t *testing.T) {
	c := newCopy(t)
	x := c.create("2026-01-01T00:00:01.250Z", tracker.NewIssue{Title: "X"})
	lkg := c.load()
	c.update("2026-01-05T00:00:00.500Z", x, tracker.Patch{DueAt: ptr(at("2026-06-01T00:00:00.125Z"))})
	ours := c.load()
	theirs := fork(t, lkg)
	setTitle(theirs, "b", x, 100, "X, renamed")
	merged, _ := merge(t, lkg, ours, theirs)

	c.store(ours, merged)
	c.wantLoads(merged)
	row := c.query(`SELECT title, due_at, created_at, updated_at, retired_at FROM issues WHERE id = ?`, x)
	want := "X, renamed|2026-06-01T00:00:00.125Z|2026-01-01T00:00:01.250Z|2026-01-05T00:00:00.500Z|<null>"
	if row[0] != want {
		t.Errorf("the row is\n%s, want\n%s", row[0], want)
	}
}

// A command that ran between the load and the write, on an issue the merge
// did not change, keeps what it did.
func TestStoreLeavesAloneWhatTheMergedSnapshotDoesNotMention(t *testing.T) {
	c := newCopy(t)
	x := c.create("2026-01-01T00:00:01Z", tracker.NewIssue{Title: "X"})
	y := c.create("2026-01-01T00:00:02Z", tracker.NewIssue{Title: "Y"})
	ours := c.load()
	theirs := fork(t, ours)
	setTitle(theirs, "b", x, 100, "X, renamed")
	merged, _ := merge(t, ours, ours, theirs)

	// While the sync was fetching and pushing:
	c.update("2026-01-05T00:00:00Z", y, tracker.Patch{Title: ptr("Y, edited meanwhile"), AddLabels: []string{"late"}})
	z := c.create("2026-01-05T00:00:01Z", tracker.NewIssue{Title: "Z, made meanwhile", BlockedBy: []string{y}})
	c.comment("2026-01-05T00:00:02Z", y, "said meanwhile")
	meanwhile := c.load()

	c.store(ours, merged)
	got := c.load()
	if got.Issues[x].Title != "X, renamed" {
		t.Errorf("x is %q", got.Issues[x].Title)
	}
	for _, id := range []string{y, z} {
		if !reflect.DeepEqual(got.Issues[id], meanwhile.Issues[id]) {
			t.Errorf("%s changed under the write:\n%+v\n%+v", id, meanwhile.Issues[id], got.Issues[id])
		}
	}
	for id := range meanwhile.Events {
		if got.Events[id] == nil {
			t.Errorf("event %s is gone", id)
		}
	}
	for id := range meanwhile.Comments {
		if got.Comments[id] == nil {
			t.Errorf("comment %s is gone", id)
		}
	}
}

// An issue the merge changed, edited between the load and the write: the
// write refuses and writes nothing, so the next sync merges the edit.
func TestStoreRefusesWhenAChangedIssueMovedSinceTheLoad(t *testing.T) {
	c := newCopy(t)
	x := c.create("2026-01-01T00:00:01Z", tracker.NewIssue{Title: "X"})
	y := c.create("2026-01-01T00:00:02Z", tracker.NewIssue{Title: "Y"})
	ours := c.load()
	theirs := fork(t, ours)
	setTitle(theirs, "b", x, 100, "X, renamed")
	setTitle(theirs, "b", y, 101, "Y, renamed")
	merged, _ := merge(t, ours, ours, theirs)

	c.update("2026-01-05T00:00:00Z", x, tracker.Patch{AddLabels: []string{"late"}})
	before := c.load()

	err := Store(c.ctx, c.db, ours, merged, syncTime)
	if !errors.Is(err, ErrChangedDuringSync) {
		t.Fatalf("Store: %v, want ErrChangedDuringSync", err)
	}
	if !strings.Contains(err.Error(), x) {
		t.Errorf("the error does not name the issue: %v", err)
	}
	c.wantLoads(before)
	if got := c.config(ConfigLastResult); got != "<unset>" {
		t.Errorf("sync.last_result = %s after a refused write", got)
	}
}

// A write that fails part-way leaves nothing behind.
func TestStoreIsAllOrNothing(t *testing.T) {
	c := newCopy(t)
	x := c.create("2026-01-01T00:00:01Z", tracker.NewIssue{Title: "X"})
	ours := c.load()
	merged := fork(t, ours)
	setTitle(merged, "b", x, 100, "X, renamed")
	merged.Issues["t-new"] = base("t-new")
	// A snapshot no merge would give: an event for an issue that is not there.
	merged.Events["zzz"] = &Event{ID: "zzz", IssueID: "t-nowhere", ChangedAt: day(1), ChangedBy: "b", Attr: "title"}

	if err := Store(c.ctx, c.db, ours, merged, syncTime); err == nil {
		t.Fatal("Store wrote an event for an issue that does not exist")
	}
	c.wantLoads(ours)
	if got := c.config(ConfigLastAt); got != "<unset>" {
		t.Errorf("sync.last_at = %s after a failed write", got)
	}
}

// A child that moved to another parent on the other copy has one parent
// at every point of the write.
func TestStoreMovesAChildToAnotherParent(t *testing.T) {
	c := newCopy(t)
	first := c.create("2026-01-01T00:00:01Z", tracker.NewIssue{Title: "First parent"})
	second := c.create("2026-01-01T00:00:02Z", tracker.NewIssue{Title: "Second parent"})
	child := c.create("2026-01-01T00:00:03Z", tracker.NewIssue{Title: "Child", Parent: first})
	ours := c.load()
	theirs := fork(t, ours)
	edit(theirs, "b", child, 100, "parent", first, second, func(i *Issue) { i.Parent = ptr(second) })
	merged, _ := merge(t, ours, ours, theirs)

	c.store(ours, merged)
	c.wantLoads(merged)
	if got := c.get(child); deref(got.Parent) != second {
		t.Errorf("the child is under %v, want %s", deref(got.Parent), second)
	}
}

// --- an issue replaced whole ------------------------------------------------

// ADR-0023: a retired record that collides with a live issue is dropped,
// with its events and comments, and the rows go with it.
func TestStoreReplacesARetiredColliderWithItsEventsAndComments(t *testing.T) {
	c := newCopy(t)
	x := c.create("2026-01-01T00:00:01Z", tracker.NewIssue{Title: "Ours, to be retired", Labels: []string{"old"}})
	other := c.create("2026-01-01T00:00:02Z", tracker.NewIssue{Title: "Other", BlockedBy: []string{x}})
	c.update("2026-01-01T00:00:03Z", x, tracker.Patch{Title: ptr("Ours, renamed")})
	c.comment("2026-01-01T00:00:04Z", x, "a remark on the old record")
	c.comment("2026-01-01T00:00:05Z", other, "a remark that stays")
	c.now = at("2026-01-01T00:00:06Z")
	if _, err := c.tr.Retire(c.ctx, []string{x}, ""); err != nil {
		t.Fatal(err)
	}
	ours := c.load()

	live := base(x)
	live.Title = "Theirs, live"
	live.CreatedBy = "someone else"
	live.CreatedAt = at("2026-01-01T00:00:01Z")
	theirs := snapOf(live)
	logEvent(theirs, "b", x, 50, "title", "Theirs", "Theirs, live")
	addComment(theirs, "b", x, 51, "a remark on the live record")

	merged, _ := merge(t, nil, ours, theirs)
	if got := merged.Issues[x]; got.CreatedBy != "someone else" {
		t.Fatalf("the merge kept %+v", got)
	}
	c.store(ours, merged)
	c.wantLoads(merged)

	if got := c.query(`SELECT text FROM comments ORDER BY id`); !reflect.DeepEqual(got,
		[]string{"a remark that stays", "a remark on the live record"}) {
		t.Errorf("comments %q", got)
	}
	if got := c.get(x); got.Title != "Theirs, live" || got.CreatedBy != "someone else" || len(got.Labels) != 0 {
		t.Errorf("the tracker shows %+v", got)
	}
}

// --- the readiness views ----------------------------------------------------

func TestReadinessViewsAgreeWithTheWrittenLinks(t *testing.T) {
	c := newCopy(t)
	blocker := c.create("2026-01-01T00:00:01Z", tracker.NewIssue{Title: "Blocker"})
	blocked := c.create("2026-01-01T00:00:02Z", tracker.NewIssue{Title: "Blocked", BlockedBy: []string{blocker}})
	parent := c.create("2026-01-01T00:00:03Z", tracker.NewIssue{Title: "Parent"})
	child := c.create("2026-01-01T00:00:04Z", tracker.NewIssue{Title: "Child"})
	grandchild := c.create("2026-01-01T00:00:05Z", tracker.NewIssue{Title: "Grandchild", Parent: child})
	loose := c.create("2026-01-01T00:00:06Z", tracker.NewIssue{Title: "Loose"})
	all := []string{blocker, blocked, parent, child, grandchild, loose}
	slices.Sort(all)

	if got, want := c.listed(tracker.Filter{Ready: true}), without(without(all, blocked), child); !reflect.DeepEqual(got, want) {
		t.Fatalf("ready before the sync: %v, want %v", got, want)
	}

	// The other copy unblocks one issue, blocks another, puts the child (and
	// so the grandchild) under a parent, and holds that parent.
	ours := c.load()
	theirs := fork(t, ours)
	removeFrom(theirs, "b", blocked, 100, "blocked_by", blocker)
	block(theirs, "b", blocker, loose, 101)
	setParent(theirs, "b", child, parent, 102)
	edit(theirs, "b", parent, 103, "status", "ready", "held", func(i *Issue) { i.Status = "held" })
	logEvent(theirs, "b", parent, 103, "status_reason", nil, "on hold")
	theirs.Issues[parent].StatusReason = ptr("on hold")
	merged, _ := merge(t, ours, ours, theirs)
	c.store(ours, merged)

	if got, want := c.listed(tracker.Filter{Ready: true}), sortedOf(blocker, blocked); !reflect.DeepEqual(got, want) {
		t.Errorf("ready after the sync: %v, want %v", got, want)
	}
	if got, want := c.listed(tracker.Filter{Blocked: true}), sortedOf(loose, child, grandchild); !reflect.DeepEqual(got, want) {
		t.Errorf("blocked after the sync: %v, want %v", got, want)
	}
	blockers, err := c.tr.ActiveBlockers(c.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string][]string{loose: {blocker}}; !reflect.DeepEqual(blockers, want) {
		t.Errorf("active blockers %v, want %v", blockers, want)
	}
	if got := c.get(loose); got.Status != "waiting" || !got.Blocked {
		t.Errorf("the newly blocked issue shows %s, blocked %v", got.Status, got.Blocked)
	}
	if got := c.get(blocked); got.Status != "ready" || got.Blocked || len(got.Links.BlockedBy) != 0 {
		t.Errorf("the unblocked issue shows %s, blocked %v, blocked by %v", got.Status, got.Blocked, got.Links.BlockedBy)
	}
	if got := c.get(grandchild); !reflect.DeepEqual(got.Ancestors, []string{child, parent}) || !got.Blocked {
		t.Errorf("the grandchild shows ancestors %v, blocked %v", got.Ancestors, got.Blocked)
	}
	if got := c.get(parent); !reflect.DeepEqual(got.Links.Children, []string{child}) {
		t.Errorf("the parent shows children %v", got.Links.Children)
	}
	// The store's own rules see the written links: the loop is refused.
	c.now = at("2026-01-06T00:00:00Z")
	_, err = c.tr.Link(c.ctx, []string{loose}, "blocks", []string{blocker})
	wantCode(t, err, tracker.CodeCycle)

	// A retire that arrives takes the issue, and its links, out of the views.
	ours = c.load()
	theirs = fork(t, ours)
	retire(theirs, "b", blocker, 200)
	merged, _ = merge(t, ours, ours, theirs)
	c.store(ours, merged)
	if got := c.get(loose); got.Status != "ready" || got.Blocked {
		t.Errorf("with its blocker retired the issue shows %s, blocked %v", got.Status, got.Blocked)
	}
	if got := c.listed(tracker.Filter{Retired: true}); !reflect.DeepEqual(got, []string{blocker}) {
		t.Errorf("retired: %v", got)
	}
}

func sortedOf(ids ...string) []string {
	slices.Sort(ids)
	return ids
}

// --- the record of the sync -------------------------------------------------

func TestStoreRecordsTheSync(t *testing.T) {
	c := newCopy(t)
	c.create("2026-01-01T00:00:01Z", tracker.NewIssue{Title: "X"})
	if got := c.config(ConfigLastAt); got != "<unset>" {
		t.Fatalf("sync.last_at = %s before any sync", got)
	}
	ours := c.load()
	// Nothing to write, and the sync still happened.
	c.store(ours, ours)
	if got := c.config(ConfigLastAt); got != "2026-02-01T12:00:00Z" {
		t.Errorf("sync.last_at = %s", got)
	}
	if got := c.config(ConfigLastResult); got != ResultOK {
		t.Errorf("sync.last_result = %s", got)
	}
	// The record is per copy: it is not in the snapshot.
	c.wantLoads(ours)

	// A sync that failed leaves the time of the last one that worked.
	if err := RecordFailure(c.ctx, c.db, "offline"); err != nil {
		t.Fatal(err)
	}
	if at, result := c.config(ConfigLastAt), c.config(ConfigLastResult); at != "2026-02-01T12:00:00Z" || result != "offline" {
		t.Errorf("after a failure: sync.last_at = %s, sync.last_result = %s", at, result)
	}
	if err := Store(c.ctx, c.db, ours, ours, syncTime.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if at, result := c.config(ConfigLastAt), c.config(ConfigLastResult); at != "2026-02-01T13:00:00Z" || result != ResultOK {
		t.Errorf("after the next sync: sync.last_at = %s, sync.last_result = %s", at, result)
	}
}

func TestStoreRefusesADatabaseThatIsNotThere(t *testing.T) {
	path := t.TempDir() + "/tess.db"
	s := snapOf(base("t-a"))
	wantCode(t, Store(context.Background(), path, s, s, syncTime), tracker.CodeNotInitialized)
	wantCode(t, RecordFailure(context.Background(), path, "offline"), tracker.CodeNotInitialized)
	if _, err := Load(context.Background(), path); err == nil {
		t.Error("Store created the database")
	}
}
