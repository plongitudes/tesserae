package sync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/plongitudes/tesserae/internal/tracker"
)

// A copy is a database driven through the tracker with a clock the test
// sets.
type testCopy struct {
	t       *testing.T
	ctx     context.Context
	db      string
	now     time.Time
	tr      tracker.Tracker
	closeDB func() error // closes the tracker's store; a test that deletes the database calls it first
}

func newCopy(t *testing.T) *testCopy {
	t.Helper()
	c := &testCopy{
		t: t, ctx: context.Background(),
		db:  filepath.Join(t.TempDir(), "tess.db"),
		now: at("2026-01-01T00:00:00Z"),
	}
	if err := tracker.Init(c.ctx, c.db, "t"); err != nil {
		t.Fatal(err)
	}
	tr, closeDB, err := tracker.Open(c.ctx, c.db, func() time.Time { return c.now }, "tester")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeDB() })
	c.tr, c.closeDB = tr, closeDB
	return c
}

// create makes an issue at the given time, so that its ID (which hashes the
// title, the creator and the time) is the same on every copy that does so.
func (c *testCopy) create(when string, n tracker.NewIssue) string {
	c.t.Helper()
	c.now = at(when)
	iss, err := c.tr.Create(c.ctx, n)
	if err != nil {
		c.t.Fatalf("create %q: %v", n.Title, err)
	}
	return iss.ID
}

func (c *testCopy) update(when, id string, p tracker.Patch) {
	c.t.Helper()
	c.now = at(when)
	if _, err := c.tr.Update(c.ctx, id, p); err != nil {
		c.t.Fatalf("update %s: %v", id, err)
	}
}

func (c *testCopy) link(when, a, wording, b string) {
	c.t.Helper()
	c.now = at(when)
	if _, err := c.tr.Link(c.ctx, []string{a}, wording, []string{b}); err != nil {
		c.t.Fatalf("link %s %s %s: %v", a, wording, b, err)
	}
}

func (c *testCopy) get(id string) tracker.Issue {
	c.t.Helper()
	iss, err := c.tr.Get(c.ctx, id, tracker.WithHistory())
	if err != nil {
		c.t.Fatalf("get %s: %v", id, err)
	}
	return iss
}

func (c *testCopy) load() *Snapshot {
	c.t.Helper()
	snap, err := Load(c.ctx, c.db)
	if err != nil {
		c.t.Fatalf("load: %v", err)
	}
	return snap
}

func deref[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestLoadHoldsWhatTheTrackerShows(t *testing.T) {
	c := newCopy(t)
	epic := c.create("2026-01-01T00:00:01Z", tracker.NewIssue{Title: "Epic", Type: "epic"})
	blocker := c.create("2026-01-01T00:00:02Z", tracker.NewIssue{Title: "Blocker"})
	work := c.create("2026-01-01T00:00:03.250Z", tracker.NewIssue{
		Title: "Work", Description: "What to do\n", Design: "How", AcceptanceCriteria: "When done", Notes: "n",
		Type: "bug", Priority: ptr(1), Assignee: "alice", Labels: []string{"zeta", "alpha"},
		Parent: epic, BlockedBy: []string{blocker},
	})
	origin := c.create("2026-01-01T00:00:04Z", tracker.NewIssue{Title: "Origin"})

	c.update("2026-01-02T00:00:00.500Z", work, tracker.Patch{
		Title: ptr("Work, renamed"), Priority: ptr(3), Description: ptr("What to do, second draft"),
		Owner: ptr("bob"), DueAt: ptr(at("2026-06-01T00:00:00Z")),
		AddLabels: []string{"beta"}, RemoveLabels: []string{"zeta"},
	})
	c.link("2026-01-02T00:00:01Z", work, "discovered-from", origin)
	c.now = at("2026-01-02T00:00:02Z")
	if _, err := c.tr.Status(c.ctx, []string{origin}, "held", "waiting on a decision", false); err != nil {
		t.Fatal(err)
	}
	c.now = at("2026-01-02T00:00:03Z")
	comment, err := c.tr.Comment(c.ctx, work, "first remark\n")
	if err != nil {
		t.Fatal(err)
	}

	snap := c.load()
	if snap.Config != (Config{Format: Format, Prefix: "t"}) {
		t.Errorf("config %+v", snap.Config)
	}
	if len(snap.Issues) != 4 {
		t.Fatalf("%d issues, want 4", len(snap.Issues))
	}

	shown := c.get(work)
	got := snap.Issues[work]
	if got == nil {
		t.Fatalf("no issue %s", work)
	}
	// The tracker shows the issue as waiting (it has an active blocker); the
	// snapshot holds the stored status.
	if shown.Status != "waiting" || got.Status != "ready" {
		t.Errorf("status: tracker shows %q, snapshot holds %q; want waiting and ready", shown.Status, got.Status)
	}
	for what, pair := range map[string][2]any{
		"id":                  {got.ID, shown.ID},
		"title":               {got.Title, shown.Title},
		"description":         {got.Description, hashOf(shown.Description)},
		"design":              {got.Design, hashOf(shown.Design)},
		"acceptance_criteria": {got.AcceptanceCriteria, hashOf(shown.AcceptanceCriteria)},
		"notes":               {got.Notes, hashOf(shown.Notes)},
		"type":                {got.Type, shown.Type},
		"priority":            {got.Priority, shown.Priority},
		"status_reason":       {deref(got.StatusReason), deref(shown.StatusReason)},
		"assignee":            {deref(got.Assignee), deref(shown.Assignee)},
		"owner":               {deref(got.Owner), deref(shown.Owner)},
		"parent":              {deref(got.Parent), deref(shown.Parent)},
		"created_by":          {got.CreatedBy, shown.CreatedBy},
		"labels":              {strings.Join(got.Labels, ","), strings.Join(shown.Labels, ",")},
		"blocked_by":          {strings.Join(got.BlockedBy, ","), strings.Join(shown.Links.BlockedBy, ",")},
		"discovered_from":     {strings.Join(got.DiscoveredFrom, ","), strings.Join(shown.Links.DiscoveredFrom, ",")},
		"due_at":              {got.DueAt.Format(time.RFC3339), shown.DueAt.Format(time.RFC3339)},
		// To the second: the database keeps milliseconds, a snapshot doesn't.
		"created_at": {got.CreatedAt, at("2026-01-01T00:00:03Z")},
		"updated_at": {got.UpdatedAt, at("2026-01-02T00:00:01Z")},
		"retired_at": {got.RetiredAt == nil, true},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s: snapshot holds %v, the tracker shows %v", what, pair[0], pair[1])
		}
	}
	if strings.Join(got.Labels, ",") != "alpha,beta" || deref(got.Parent) != epic {
		t.Errorf("labels %v, parent %v", got.Labels, deref(got.Parent))
	}

	// Every text any field has held is there, with its body, and the empty one.
	for _, body := range []string{"", "What to do\n", "What to do, second draft", "How", "When done", "n"} {
		if stored, ok := snap.Texts[hashOf(body)]; !ok || stored != body {
			t.Errorf("text %q: stored as %q, present %v", body, stored, ok)
		}
	}

	// Events: the ones the tracker shows, with the JSON values.
	if len(shown.History) == 0 {
		t.Fatal("the tracker shows no history")
	}
	events := 0
	for _, ev := range snap.Events {
		if ev.IssueID == work {
			events++
		}
	}
	if events != len(shown.History) {
		t.Errorf("%d events on %s, the tracker shows %d", events, work, len(shown.History))
	}
	for _, want := range shown.History {
		ev := snap.Events[want.ID]
		if ev == nil {
			t.Errorf("no event %s (%s)", want.ID, want.Attr)
			continue
		}
		if ev.IssueID != work || ev.Attr != want.Attr || ev.ChangedBy != want.ChangedBy ||
			!ev.ChangedAt.Equal(want.ChangedAt.Truncate(time.Second)) {
			t.Errorf("event %s: %+v, the tracker shows %+v", want.ID, ev, want)
		}
		if ev.Attr == "priority" && (ev.OldValue != float64(1) || ev.NewValue != float64(3)) {
			t.Errorf("priority event values %#v -> %#v, want float64 1 -> 3", ev.OldValue, ev.NewValue)
		}
		if ev.Attr == "owner" && (ev.OldValue != nil || ev.NewValue != "bob") {
			t.Errorf("owner event values %#v -> %#v, want nil -> bob", ev.OldValue, ev.NewValue)
		}
	}

	// The held issue: status and reason as stored.
	if o := snap.Issues[origin]; o.Status != "held" || deref(o.StatusReason) != "waiting on a decision" {
		t.Errorf("origin: status %q reason %v", o.Status, deref(o.StatusReason))
	}

	// The comment.
	cm := snap.Comments[comment.ID]
	if len(snap.Comments) != 1 || cm == nil || cm.IssueID != work || cm.Author != "tester" ||
		cm.Text != "first remark\n" || !cm.CreatedAt.Equal(at("2026-01-02T00:00:03Z")) {
		t.Errorf("comments %d, comment %+v", len(snap.Comments), cm)
	}
}

func TestLoadHoldsRetiredIssuesAndTheirLinks(t *testing.T) {
	c := newCopy(t)
	parent := c.create("2026-01-01T00:00:01Z", tracker.NewIssue{Title: "Parent"})
	child := c.create("2026-01-01T00:00:02Z", tracker.NewIssue{Title: "Child", Parent: parent})
	c.now = at("2026-01-03T00:00:00Z")
	if _, err := c.tr.Status(c.ctx, []string{child}, "done", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := c.tr.Retire(c.ctx, []string{parent}, ""); err != nil {
		t.Fatal(err)
	}

	snap := c.load()
	p := snap.Issues[parent]
	if p == nil || p.RetiredAt == nil || !p.RetiredAt.Equal(at("2026-01-03T00:00:00Z")) {
		t.Fatalf("the retired issue reads as %+v", p)
	}
	// The tracker shows the child without a parent while the parent is
	// retired; the link is still stored, and the snapshot holds what is stored.
	if shown := c.get(child); shown.Parent != nil {
		t.Fatalf("the tracker shows the child under %s", *shown.Parent)
	}
	if got := deref(snap.Issues[child].Parent); got != parent {
		t.Errorf("the child's parent in the snapshot is %v, want %s", got, parent)
	}
}

func TestLoadPutsEachLinkOnTheIssueThatRecordsIt(t *testing.T) {
	c := newCopy(t)
	a := c.create("2026-01-01T00:00:01Z", tracker.NewIssue{Title: "A"})
	b := c.create("2026-01-01T00:00:02Z", tracker.NewIssue{Title: "B"})
	small, large := min(a, b), max(a, b)
	// Each given from the side that does not record it.
	c.link("2026-01-02T00:00:00Z", large, "related", small)
	c.link("2026-01-02T00:00:01Z", a, "blocks", b)
	c.link("2026-01-02T00:00:02Z", a, "discovered-from", b)

	snap := c.load()
	want := map[string][]string{
		small + " related":     {large},
		large + " related":     {},
		b + " blocked_by":      {a},
		a + " blocked_by":      {},
		a + " discovered_from": {b},
		b + " discovered_from": {},
	}
	got := map[string][]string{}
	for _, id := range []string{a, b} {
		i := snap.Issues[id]
		got[id+" related"], got[id+" blocked_by"], got[id+" discovered_from"] = i.Related, i.BlockedBy, i.DiscoveredFrom
	}
	for k, w := range want {
		if g := got[k]; g == nil || !slices.Equal(g, w) {
			t.Errorf("%s: %v, want %v", k, g, w)
		}
	}
}

// build makes the same four issues on a copy in one of two orders, with
// labels and blockers given in different orders, and nothing that logs an
// event (events and comments carry random IDs, so no two copies make the
// same ones).
func build(c *testCopy, variant int) (a, b, cc, d string) {
	newA := func() { a = c.create("2026-01-01T00:00:01Z", tracker.NewIssue{Title: "A", Description: "shared text"}) }
	newB := func() {
		labels := [][]string{{"x", "y"}, {"y", "x", "y"}}[variant]
		b = c.create("2026-01-01T00:00:02Z", tracker.NewIssue{Title: "B", Labels: labels, BlockedBy: []string{a}})
	}
	newC := func() {
		cc = c.create("2026-01-01T00:00:03Z", tracker.NewIssue{Title: "C", Parent: a, Notes: "shared text"})
	}
	newD := func() {
		blockers := [][]string{{b, cc}, {cc, b}}[variant]
		d = c.create("2026-01-01T00:00:04Z", tracker.NewIssue{Title: "D", BlockedBy: blockers})
	}
	if variant == 0 {
		newA()
		newB()
		newC()
		newD()
	} else {
		newA()
		newC()
		newB()
		newD()
	}
	return
}

func TestTheSameStateReachedInDifferentOrdersGivesIdenticalFiles(t *testing.T) {
	one, two := newCopy(t), newCopy(t)
	a1, b1, c1, d1 := build(one, 0)
	a2, b2, c2, d2 := build(two, 1)
	if a1 != a2 || b1 != b2 || c1 != c2 || d1 != d2 {
		t.Fatalf("the copies minted different IDs: %v, %v", []string{a1, b1, c1, d1}, []string{a2, b2, c2, d2})
	}
	first, second := one.load().Files(), two.load().Files()
	if len(first) != 1+4+2 { // config, four issues, the empty text and the shared one
		t.Errorf("%d files, want 7", len(first))
	}
	wantSameFiles(t, first, second)
}

func TestTheSameEditsInDifferentOrdersGiveIdenticalIssueFiles(t *testing.T) {
	one, two := newCopy(t), newCopy(t)
	a, b, c, d := build(one, 0)
	build(two, 1)
	small, large := min(c, d), max(c, d)

	const when = "2026-01-05T00:00:00Z"
	one.update(when, a, tracker.Patch{AddLabels: []string{"p", "q"}, Title: ptr("A2")})
	one.link(when, small, "related", large)
	one.link(when, b, "discovered-from", a)
	one.link(when, d, "blocked-by", a)

	two.link(when, a, "blocks", d)
	two.link(when, small, "related", large)
	two.update(when, a, tracker.Patch{Title: ptr("A2")})
	two.link(when, b, "discovered-from", a)
	two.update(when, a, tracker.Patch{AddLabels: []string{"q"}})
	two.update(when, a, tracker.Patch{AddLabels: []string{"p"}})

	// Events carry random IDs, so only the state is compared: everything but
	// the event files.
	state := func(c *testCopy) map[string][]byte {
		files := c.load().Files()
		events := 0
		for path := range files {
			if strings.HasPrefix(path, "events/") {
				delete(files, path)
				events++
			}
		}
		if events != 6 {
			t.Errorf("%d event files, want 6", events)
		}
		return files
	}
	wantSameFiles(t, state(one), state(two))
}

// A related link given in opposite wordings is the same link in the
// snapshot. (The two copies' files still differ in updated_at: the tracker
// logs the link, and moves updated_at, on the issue it was given from.)
func TestRelatedInOppositeWordingsIsTheSameLink(t *testing.T) {
	one, two := newCopy(t), newCopy(t)
	a, b, _, _ := build(one, 0)
	build(two, 0)
	small, large := min(a, b), max(a, b)
	one.link("2026-01-05T00:00:00Z", small, "related", large)
	two.link("2026-01-05T00:00:00Z", large, "related", small)
	for name, c := range map[string]*testCopy{"one": one, "two": two} {
		snap := c.load()
		if got := snap.Issues[small].Related; !slices.Equal(got, []string{large}) {
			t.Errorf("copy %s: the smaller ID lists %v, want [%s]", name, got, large)
		}
		if got := snap.Issues[large].Related; len(got) != 0 {
			t.Errorf("copy %s: the larger ID lists %v, want none", name, got)
		}
	}
}

func TestLoadedFilesRoundTrip(t *testing.T) {
	c := newCopy(t)
	a, b, _, _ := build(c, 0)
	c.update("2026-01-05T00:00:00.123Z", a, tracker.Patch{Priority: ptr(0), Description: ptr("<b>bold</b> & more\n")})
	c.link("2026-01-05T00:00:01Z", a, "related", b)
	c.now = at("2026-01-05T00:00:02Z")
	if _, err := c.tr.Comment(c.ctx, a, "a remark"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.tr.Retire(c.ctx, []string{b}, ""); err != nil {
		t.Fatal(err)
	}

	first := c.load().Files()
	parsed, err := FromFiles(first)
	if err != nil {
		t.Fatal(err)
	}
	wantSameFiles(t, first, parsed.Files())
	// And loading twice gives the same bytes.
	wantSameFiles(t, first, c.load().Files())
}

func TestLoadRefusesADatabaseThatIsNotThere(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tess.db")
	_, err := Load(context.Background(), path)
	var e *tracker.Error
	if !errors.As(err, &e) || e.Code != tracker.CodeNotInitialized {
		t.Fatalf("got %v, want %s", err, tracker.CodeNotInitialized)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Load created the database: %v", err)
	}
}
