package tracker_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/plongitudes/tesserae/internal/tracker"
)

// link makes one link, between a and b.
func (f *fixture) link(a, wording, b string) tracker.Link {
	f.t.Helper()
	links, err := f.tr.Link(f.ctx, []string{a}, wording, []string{b})
	if err != nil {
		f.t.Fatalf("Link(%s %s %s): %v", a, wording, b, err)
	}
	if len(links) != 1 {
		f.t.Fatalf("Link(%s %s %s) returned %d links; want 1", a, wording, b, len(links))
	}
	return links[0]
}

// unlink removes one link, between a and b.
func (f *fixture) unlink(a, wording, b string) tracker.Link {
	f.t.Helper()
	links, err := f.tr.Unlink(f.ctx, []string{a}, wording, []string{b})
	if err != nil {
		f.t.Fatalf("Unlink(%s %s %s): %v", a, wording, b, err)
	}
	if len(links) != 1 {
		f.t.Fatalf("Unlink(%s %s %s) returned %d links; want 1", a, wording, b, len(links))
	}
	return links[0]
}

func wantLinks(t *testing.T, got []tracker.Link, want ...tracker.Link) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("links = %+v; want %+v", got, want)
	}
}

func wantLink(t *testing.T, got tracker.Link, from, typ, to string) {
	t.Helper()
	if want := (tracker.Link{From: from, Type: typ, To: to}); got != want {
		t.Errorf("link = %+v; want %+v", got, want)
	}
}

func TestBlocksAndBlockedByAreTheSameLink(t *testing.T) {
	for _, wording := range []string{"blocks", "blocked-by"} {
		t.Run(wording, func(t *testing.T) {
			f := newFixture(t)
			a, b := f.titled("Blocker"), f.titled("Dependent")
			var got tracker.Link
			if wording == "blocks" {
				got = f.link(a, "blocks", b)
			} else {
				got = f.link(b, "blocked-by", a)
			}
			wantLink(t, got, a, "blocks", b)
			wantStrings(t, "blocker blocks", f.get(a).Links.Blocks, b)
			wantStrings(t, "dependent blocked_by", f.get(b).Links.BlockedBy, a)
			if d := f.get(b); d.Status != "waiting" || !d.Blocked {
				t.Errorf("dependent: status %s, blocked %v; want waiting, true", d.Status, d.Blocked)
			}
			// Logged once, on the dependent.
			f.wantChanges(b, change{"blocked_by", nil, a})
			f.wantChanges(a)
		})
	}
}

func TestParentOfAndChildOfAreTheSameLink(t *testing.T) {
	for _, wording := range []string{"parent-of", "child-of", "parent"} {
		t.Run(wording, func(t *testing.T) {
			f := newFixture(t)
			p, c := f.titled("Parent"), f.titled("Child")
			var got tracker.Link
			if wording == "child-of" {
				got = f.link(c, wording, p)
			} else {
				got = f.link(p, wording, c)
			}
			wantLink(t, got, p, "parent", c)
			child := f.get(c)
			if child.Parent == nil || *child.Parent != p {
				t.Errorf("child parent = %v; want %s", child.Parent, p)
			}
			wantStrings(t, "parent children", f.get(p).Links.Children, c)
			// Logged once, on the child, as a scalar.
			f.wantChanges(c, change{"parent", nil, p})
			f.wantChanges(p)
		})
	}
}

func TestRelatedIsSymmetric(t *testing.T) {
	f := newFixture(t)
	a, b := f.titled("A"), f.titled("B")
	wantLink(t, f.link(a, "related", b), a, "related", b)
	wantStrings(t, "A related", f.get(a).Links.Related, b)
	wantStrings(t, "B related", f.get(b).Links.Related, a)
	f.wantChanges(a, change{"related", nil, b})
	f.wantChanges(b)

	// Given the other way round, it is the link that's already there.
	wantLink(t, f.link(b, "related", a), a, "related", b)
	wantStrings(t, "A related", f.get(a).Links.Related, b)
	f.wantChanges(a, change{"related", nil, b})
	f.wantChanges(b)
}

func TestDiscoveredFromIsDirected(t *testing.T) {
	f := newFixture(t)
	origin, found := f.titled("Origin"), f.titled("Found")
	wantLink(t, f.link(found, "discovered-from", origin), found, "discovered-from", origin)
	wantStrings(t, "found discovered_from", f.get(found).Links.DiscoveredFrom, origin)
	wantStrings(t, "origin discovered", f.get(origin).Links.Discovered, found)
	wantStrings(t, "origin discovered_from", f.get(origin).Links.DiscoveredFrom)
	f.wantChanges(found, change{"discovered_from", nil, origin})
	f.wantChanges(origin)

	// The other direction is a different link, and both can exist.
	wantLink(t, f.link(origin, "discovered-from", found), origin, "discovered-from", found)
	wantStrings(t, "origin discovered_from", f.get(origin).Links.DiscoveredFrom, found)
}

func TestLinkingTwiceInEitherWordingIsANoOp(t *testing.T) {
	f := newFixture(t)
	a, b := f.titled("A"), f.titled("B")
	p, c := f.titled("P"), f.titled("C")
	f.link(a, "blocks", b)
	f.link(p, "parent-of", c)
	f.link(c, "discovered-from", a)
	before := f.get(b).UpdatedAt

	f.tick()
	wantLink(t, f.link(a, "blocks", b), a, "blocks", b)
	wantLink(t, f.link(b, "blocked-by", a), a, "blocks", b)
	wantLink(t, f.link(p, "parent-of", c), p, "parent", c)
	wantLink(t, f.link(c, "child-of", p), p, "parent", c)
	wantLink(t, f.link(c, "discovered-from", a), c, "discovered-from", a)

	wantStrings(t, "A blocks", f.get(a).Links.Blocks, b)
	wantStrings(t, "P children", f.get(p).Links.Children, c)
	f.wantChanges(b, change{"blocked_by", nil, a})
	f.wantChanges(c, change{"parent", nil, p}, change{"discovered_from", nil, a})
	if got := f.get(b).UpdatedAt; !got.Equal(before) {
		t.Errorf("updated_at moved to %v on a no-op link", got)
	}
}

func TestALinkMovesUpdatedAtOnTheSideItIsLoggedOn(t *testing.T) {
	f := newFixture(t)
	a, b := f.titled("A"), f.titled("B")
	f.tick()
	f.link(a, "blocks", b)
	if got := f.get(b).UpdatedAt; !got.Equal(f.now) {
		t.Errorf("dependent updated_at = %v; want %v", got, f.now)
	}
	if got := f.get(a).UpdatedAt; !got.Equal(start) {
		t.Errorf("blocker updated_at = %v; want %v", got, start)
	}
}

func TestUnlinkAcceptsEveryWording(t *testing.T) {
	f := newFixture(t)
	a, b := f.titled("A"), f.titled("B")

	f.link(a, "blocks", b)
	wantLink(t, f.unlink(b, "blocked-by", a), a, "blocks", b)
	wantStrings(t, "A blocks", f.get(a).Links.Blocks)
	if d := f.get(b); d.Status != "ready" || d.Blocked {
		t.Errorf("dependent after unlink: status %s, blocked %v; want ready, false", d.Status, d.Blocked)
	}
	f.link(b, "blocked-by", a)
	wantLink(t, f.unlink(a, "blocks", b), a, "blocks", b)
	f.wantChanges(b,
		change{"blocked_by", nil, a}, change{"blocked_by", a, nil},
		change{"blocked_by", nil, a}, change{"blocked_by", a, nil})
	f.wantChanges(a)

	p, c := f.titled("P"), f.titled("C")
	f.link(p, "parent-of", c)
	wantLink(t, f.unlink(c, "child-of", p), p, "parent", c)
	if got := f.get(c).Parent; got != nil {
		t.Errorf("child parent = %v after unlink; want none", *got)
	}
	f.link(c, "child-of", p)
	wantLink(t, f.unlink(p, "parent-of", c), p, "parent", c)
	f.wantChanges(c,
		change{"parent", nil, p}, change{"parent", p, nil},
		change{"parent", nil, p}, change{"parent", p, nil})
	f.wantChanges(p)

	// A related link comes off from either end.
	x, y := f.titled("X"), f.titled("Y")
	f.link(x, "related", y)
	wantLink(t, f.unlink(y, "related", x), x, "related", y)
	wantStrings(t, "X related", f.get(x).Links.Related)
	wantStrings(t, "Y related", f.get(y).Links.Related)
	f.wantChanges(x, change{"related", nil, y}, change{"related", y, nil})
	f.wantChanges(y)

	f.link(x, "discovered-from", y)
	wantLink(t, f.unlink(x, "discovered-from", y), x, "discovered-from", y)
	wantStrings(t, "X discovered_from", f.get(x).Links.DiscoveredFrom)
	f.wantChanges(x, change{"related", nil, y}, change{"related", y, nil},
		change{"discovered_from", nil, y}, change{"discovered_from", y, nil})
}

func TestUnlinkingALinkThatIsNotThereIsRefused(t *testing.T) {
	f := newFixture(t)
	a, b := f.titled("A"), f.titled("B")
	_, err := f.tr.Unlink(f.ctx, []string{a}, "blocks", []string{b})
	wantCode(t, err, tracker.CodeNotFound)

	// The same pair the other way round is a different link.
	f.link(a, "blocks", b)
	_, err = f.tr.Unlink(f.ctx, []string{b}, "blocks", []string{a})
	wantCode(t, err, tracker.CodeNotFound)
	_, err = f.tr.Unlink(f.ctx, []string{a}, "related", []string{b})
	wantCode(t, err, tracker.CodeNotFound)
	wantStrings(t, "A blocks", f.get(a).Links.Blocks, b)
}

func TestLinkRefusesUnknownWordingsAndIssues(t *testing.T) {
	f := newFixture(t)
	a, b := f.titled("A"), f.titled("B")
	for _, wording := range []string{"duplicates", "supersedes", "parent-child", "discovered", ""} {
		_, err := f.tr.Link(f.ctx, []string{a}, wording, []string{b})
		wantCode(t, err, tracker.CodeInvalidArgument)
		_, err = f.tr.Unlink(f.ctx, []string{a}, wording, []string{b})
		wantCode(t, err, tracker.CodeInvalidArgument)
	}
	_, err := f.tr.Link(f.ctx, []string{a}, "blocks", []string{"t-zzzzzzzz"})
	wantCode(t, err, tracker.CodeNotFound)
	_, err = f.tr.Link(f.ctx, []string{"t-zzzzzzzz"}, "blocks", []string{a})
	wantCode(t, err, tracker.CodeNotFound)
	_, err = f.tr.Link(f.ctx, []string{"t-"}, "blocks", []string{a}) // matches both issues
	wantCode(t, err, tracker.CodeAmbiguousID)
}

func TestLinkResolvesPartialIDs(t *testing.T) {
	f := newFixture(t)
	a, b := f.titled("A"), f.titled("B")
	if a[2:] == b[2:] {
		t.Fatalf("two issues share a hash: %s %s", a, b)
	}
	wantLink(t, f.link(a[2:], "blocks", b[2:]), a, "blocks", b)
}

func TestLinkingToARetiredIssueIsRefused(t *testing.T) {
	f := newFixture(t)
	a, b := f.titled("A"), f.titled("B")
	f.retire(a)
	for _, wording := range []string{"blocks", "blocked-by", "parent-of", "child-of", "related", "discovered-from"} {
		_, err := f.tr.Link(f.ctx, []string{a}, wording, []string{b})
		wantCode(t, err, tracker.CodeRetired)
		_, err = f.tr.Link(f.ctx, []string{b}, wording, []string{a})
		wantCode(t, err, tracker.CodeRetired)
	}
	f.wantChanges(b)
}

func (f *fixture) wantLinkRefused(a, wording, b, code string) {
	f.t.Helper()
	_, err := f.tr.Link(f.ctx, []string{a}, wording, []string{b})
	if err == nil {
		f.t.Errorf("Link(%s %s %s) succeeded; want %s", a, wording, b, code)
		return
	}
	wantCode(f.t, err, code)
}

func TestALinkFromAnIssueToItselfIsRefused(t *testing.T) {
	f := newFixture(t)
	a := f.titled("A")
	for _, wording := range []string{"blocks", "blocked-by", "parent-of", "child-of"} {
		f.wantLinkRefused(a, wording, a, tracker.CodeCycle)
	}
	// Not a cycle, since these types can't form one, but still no link.
	for _, wording := range []string{"related", "discovered-from"} {
		f.wantLinkRefused(a, wording, a, tracker.CodeInvalidArgument)
	}
	f.wantChanges(a)
}

func TestBlocksCyclesAreRefused(t *testing.T) {
	f := newFixture(t)
	x, y, z := f.titled("X"), f.titled("Y"), f.titled("Z")
	f.link(x, "blocks", y)

	// Two issues blocking each other, in either wording.
	f.wantLinkRefused(y, "blocks", x, tracker.CodeCycle)
	f.wantLinkRefused(x, "blocked-by", y, tracker.CodeCycle)

	// A longer loop.
	f.link(y, "blocks", z)
	f.wantLinkRefused(z, "blocks", x, tracker.CodeCycle)
	f.wantLinkRefused(x, "blocked-by", z, tracker.CodeCycle)

	// A second path between two issues is not a loop.
	f.link(x, "blocks", z)
	wantStrings(t, "X blocked_by", f.get(x).Links.BlockedBy)
	f.wantChanges(x)
}

func TestParentCyclesAreRefused(t *testing.T) {
	f := newFixture(t)
	top, mid, leaf := f.titled("Top"), f.titled("Mid"), f.titled("Leaf")
	f.link(top, "parent-of", mid)
	f.link(mid, "parent-of", leaf)

	f.wantLinkRefused(mid, "parent-of", top, tracker.CodeCycle)
	f.wantLinkRefused(leaf, "parent-of", top, tracker.CodeCycle)
	f.wantLinkRefused(top, "child-of", leaf, tracker.CodeCycle)
	if got := f.get(top).Parent; got != nil {
		t.Errorf("top parent = %s; want none", *got)
	}
}

// Blocks and parent links hold issues up together, so a loop through both
// kinds is a deadlock all the same.
func TestCyclesThroughBothBlocksAndParentAreRefused(t *testing.T) {
	f := newFixture(t)
	p, c := f.titled("Parent"), f.titled("Child")
	f.link(p, "parent-of", c)
	// The parent would be blocked by its child, which is blocked with it.
	f.wantLinkRefused(c, "blocks", p, tracker.CodeCycle)

	x, y, z := f.titled("X"), f.titled("Y"), f.titled("Z")
	f.link(x, "blocks", y)
	f.link(y, "parent-of", z)
	f.wantLinkRefused(z, "blocks", x, tracker.CodeCycle)
	f.wantLinkRefused(z, "parent-of", x, tracker.CodeCycle)
}

func TestRelatedAndDiscoveredFromTakeNoPartInCycles(t *testing.T) {
	f := newFixture(t)
	x, z := f.titled("X"), f.titled("Z")
	f.link(x, "blocks", z)
	f.link(z, "related", x)
	f.link(x, "discovered-from", z)
	f.link(z, "discovered-from", x)

	// And they don't close one: with A related B, B can still block A.
	a, b := f.titled("A"), f.titled("B")
	f.link(a, "related", b)
	f.link(a, "discovered-from", b)
	f.link(b, "blocks", a)

	c, d := f.titled("C"), f.titled("D")
	f.link(c, "related", d)
	f.link(c, "discovered-from", d)
	f.link(d, "parent-of", c)
}

func TestALoopThroughARetiredIssueIsRefused(t *testing.T) {
	f := newFixture(t)
	x, r, y := f.titled("X"), f.titled("R"), f.titled("Y")
	f.link(x, "blocks", r)
	f.link(r, "blocks", y)
	f.retire(r)
	// Restoring R would close the loop.
	f.wantLinkRefused(y, "blocks", x, tracker.CodeCycle)
}

func TestAnIssueHasOneParent(t *testing.T) {
	f := newFixture(t)
	p, q, c := f.titled("P"), f.titled("Q"), f.titled("C")
	f.link(p, "parent-of", c)
	f.wantLinkRefused(q, "parent-of", c, tracker.CodeHasParent)
	f.wantLinkRefused(c, "child-of", q, tracker.CodeHasParent)
	if got := f.get(c).Parent; got == nil || *got != p {
		t.Errorf("child parent = %v; want %s", got, p)
	}

	// Reparent: unlink, then link.
	f.unlink(p, "parent-of", c)
	wantLink(t, f.link(c, "child-of", q), q, "parent", c)
	if got := f.get(c).Parent; got == nil || *got != q {
		t.Errorf("child parent = %v; want %s", got, q)
	}
	f.wantChanges(c, change{"parent", nil, p}, change{"parent", p, nil}, change{"parent", nil, q})
}

func TestAChildOfARetiredParentStillHasAParentUntilUnlinked(t *testing.T) {
	f := newFixture(t)
	p, q, c := f.titled("P"), f.titled("Q"), f.titled("C")
	f.link(p, "parent-of", c)
	f.retire(p)
	if got := f.get(c).Parent; got != nil {
		t.Errorf("child parent = %s while its parent is retired; want none", *got)
	}
	// The link is still there and would come back on restore.
	f.wantLinkRefused(q, "parent-of", c, tracker.CodeHasParent)

	// The link is the child's, so it can be taken off while the parent is
	// retired; the retired parent needs its exact ID.
	_, err := f.tr.Unlink(f.ctx, []string{p[2:]}, "parent-of", []string{c})
	wantCode(t, err, tracker.CodeNotFound)
	wantLink(t, f.unlink(p, "parent-of", c), p, "parent", c)
	f.link(q, "parent-of", c)
	f.wantChanges(c, change{"parent", nil, p}, change{"parent", p, nil}, change{"parent", nil, q})
	f.wantChanges(p, change{"retired_at", nil, "2026-01-01T00:00:00Z"})
}

func TestUnlinkRefusesToChangeARetiredIssue(t *testing.T) {
	f := newFixture(t)
	p, c := f.titled("P"), f.titled("C")
	a, b := f.titled("A"), f.titled("B")
	f.link(p, "parent-of", c)
	f.link(a, "blocks", b)
	f.retire(c)
	f.retire(b)
	// These links are logged on the retired child and dependent.
	_, err := f.tr.Unlink(f.ctx, []string{p}, "parent-of", []string{c})
	wantCode(t, err, tracker.CodeRetired)
	_, err = f.tr.Unlink(f.ctx, []string{b}, "blocked-by", []string{a})
	wantCode(t, err, tracker.CodeRetired)
	f.wantChanges(c, change{"parent", nil, p}, change{"retired_at", nil, "2026-01-01T00:00:00Z"})
	f.wantChanges(b, change{"blocked_by", nil, a}, change{"retired_at", nil, "2026-01-01T00:00:00Z"})
}

func TestLinkingSeveralIssuesOnEitherSideLinksEveryPairInOrder(t *testing.T) {
	f := newFixture(t)
	a, b, c, d := f.titled("A"), f.titled("B"), f.titled("C"), f.titled("D")
	got, err := f.tr.Link(f.ctx, []string{a, b[2:]}, "blocks", []string{c, d})
	if err != nil {
		t.Fatal(err)
	}
	wantLinks(t, got,
		tracker.Link{From: a, Type: "blocks", To: c},
		tracker.Link{From: a, Type: "blocks", To: d},
		tracker.Link{From: b, Type: "blocks", To: c},
		tracker.Link{From: b, Type: "blocks", To: d})
	wantStrings(t, "c blocked_by", f.get(c).Links.BlockedBy, sorted(a, b)...)
	wantStrings(t, "d blocked_by", f.get(d).Links.BlockedBy, sorted(a, b)...)
}

func TestSeveralIssuesInAnInverseWordingKeepTheOrderGiven(t *testing.T) {
	f := newFixture(t)
	a, b, p := f.titled("A"), f.titled("B"), f.titled("P")
	got, err := f.tr.Link(f.ctx, []string{a, b}, "child-of", []string{p})
	if err != nil {
		t.Fatal(err)
	}
	wantLinks(t, got,
		tracker.Link{From: p, Type: "parent", To: a},
		tracker.Link{From: p, Type: "parent", To: b})
}

func TestOneRefusalAmongSeveralLinksLinksNothing(t *testing.T) {
	f := newFixture(t)
	a, b, c := f.titled("A"), f.titled("B"), f.titled("C")
	_, err := f.tr.Link(f.ctx, []string{a}, "blocks", []string{b, "t-zzzzzzzz"})
	wantCode(t, err, tracker.CodeNotFound)
	wantStrings(t, "a blocks", f.get(a).Links.Blocks)

	// The second pair is refused because of the first: both are undone.
	_, err = f.tr.Link(f.ctx, []string{a, b}, "parent-of", []string{c})
	wantCode(t, err, tracker.CodeHasParent)
	if parent := f.get(c).Parent; parent != nil {
		t.Errorf("c has parent %s after a refused link", *parent)
	}
	f.wantChanges(c)
}

func TestAnIssueNamedTwiceOnOneSideIsLinkedOnce(t *testing.T) {
	f := newFixture(t)
	a, b := f.titled("A"), f.titled("B")
	got, err := f.tr.Link(f.ctx, []string{a, a[2:]}, "blocks", []string{b, b})
	if err != nil {
		t.Fatal(err)
	}
	wantLinks(t, got, tracker.Link{From: a, Type: "blocks", To: b})
	got, err = f.tr.Unlink(f.ctx, []string{a, a}, "blocks", []string{b})
	if err != nil {
		t.Fatal(err)
	}
	wantLinks(t, got, tracker.Link{From: a, Type: "blocks", To: b})
}

func TestLinkingNeedsAnIssueOnEachSide(t *testing.T) {
	f := newFixture(t)
	a := f.titled("A")
	_, err := f.tr.Link(f.ctx, nil, "blocks", []string{a})
	wantCode(t, err, tracker.CodeInvalidArgument)
	_, err = f.tr.Unlink(f.ctx, []string{a}, "blocks", nil)
	wantCode(t, err, tracker.CodeInvalidArgument)
}

func TestUnlinkingSeveralIssuesRemovesEveryPairOrNone(t *testing.T) {
	f := newFixture(t)
	a, b, c, d := f.titled("A"), f.titled("B"), f.titled("C"), f.titled("D")
	if _, err := f.tr.Link(f.ctx, []string{a, b}, "blocks", []string{c, d}); err != nil {
		t.Fatal(err)
	}
	f.unlink(b, "blocks", d)

	// b no longer blocks d, so the whole removal is refused.
	_, err := f.tr.Unlink(f.ctx, []string{a, b}, "blocks", []string{c, d})
	wantCode(t, err, tracker.CodeNotFound)
	wantStrings(t, "c blocked_by", f.get(c).Links.BlockedBy, sorted(a, b)...)

	got, err := f.tr.Unlink(f.ctx, []string{c}, "blocked-by", []string{a, b})
	if err != nil {
		t.Fatal(err)
	}
	wantLinks(t, got,
		tracker.Link{From: a, Type: "blocks", To: c},
		tracker.Link{From: b, Type: "blocks", To: c})
	wantStrings(t, "c blocked_by", f.get(c).Links.BlockedBy)
}

func sorted(ids ...string) []string {
	slices.Sort(ids)
	return ids
}

// cycleRefusal makes a link that must be refused with code `cycle`, and
// returns the refusal.
func (f *fixture) cycleRefusal(a, wording, b string) *tracker.Error {
	f.t.Helper()
	_, err := f.tr.Link(f.ctx, []string{a}, wording, []string{b})
	var refusal *tracker.Error
	if !errors.As(err, &refusal) || refusal.Code != tracker.CodeCycle {
		f.t.Fatalf("Link(%s %s %s) = %v; want a cycle refusal", a, wording, b, err)
	}
	return refusal
}

func wantMessage(t *testing.T, e *tracker.Error, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if !strings.Contains(e.Message, part) {
			t.Errorf("message %q; want it to say %q", e.Message, part)
		}
	}
}

// ADR-0016: a parent already waits on its open children, and a blocked
// ancestor blocks its descendants, so the child could never start.
func TestAChildBlockingItsParentIsRefusedInHierarchyTerms(t *testing.T) {
	f := newFixture(t)
	p, c := f.titled("Parent"), f.titled("Child")
	f.link(p, "parent-of", c)
	for _, refusal := range []*tracker.Error{f.cycleRefusal(c, "blocks", p), f.cycleRefusal(p, "blocked-by", c)} {
		wantMessage(t, refusal, c+" is a child of "+p, "a parent's open children already hold it", "dependency loop")
	}
	wantStrings(t, "child blocks", f.get(c).Links.Blocks)
	f.wantChanges(p)
}

// ADR-0016: the child waits on the parent, which can't finish while the
// child is open.
func TestAParentBlockingItsChildIsRefusedInHierarchyTerms(t *testing.T) {
	f := newFixture(t)
	p, c := f.titled("Parent"), f.titled("Child")
	f.link(p, "parent-of", c)
	for _, refusal := range []*tracker.Error{f.cycleRefusal(p, "blocks", c), f.cycleRefusal(c, "blocked-by", p)} {
		wantMessage(t, refusal, p+" is an ancestor of "+c, "so "+c+" could never start", "dependency loop")
	}
	if got := f.get(c); got.Blocked || len(got.Links.BlockedBy) != 0 {
		t.Errorf("child: blocked %v, blocked_by %v; want neither", got.Blocked, got.Links.BlockedBy)
	}
	f.wantChanges(c, change{"parent", nil, p})
}

func TestBlocksBetweenAnIssueAndItsGrandchildIsRefusedBothWays(t *testing.T) {
	f := newFixture(t)
	top, mid, leaf := f.titled("Top"), f.titled("Mid"), f.titled("Leaf")
	f.link(top, "parent-of", mid)
	f.link(mid, "parent-of", leaf)

	wantMessage(t, f.cycleRefusal(leaf, "blocks", top),
		leaf+" is a descendant of "+top, "a parent's open children already hold it", "dependency loop")
	wantMessage(t, f.cycleRefusal(top, "blocks", leaf),
		top+" is an ancestor of "+leaf, "so "+leaf+" could never start", "dependency loop")

	// Siblings and cousins are not each other's ancestors.
	other := f.titled("Other child")
	f.link(top, "parent-of", other)
	f.link(other, "blocks", mid)
	f.link(other, "blocks", leaf)
}

// The hierarchy holds whether or not an issue in it is retired: the links
// are still there and count again on restore.
func TestBlocksAcrossARetiredParentIsRefused(t *testing.T) {
	f := newFixture(t)
	top, mid, leaf := f.titled("Top"), f.titled("Mid"), f.titled("Leaf")
	f.link(top, "parent-of", mid)
	f.link(mid, "parent-of", leaf)
	f.retire(mid)
	f.cycleRefusal(top, "blocks", leaf)
	f.cycleRefusal(leaf, "blocks", top)
}

// The same rule from the other side: a parent link that would put a blocks
// link between an ancestor and its descendant.
func TestAParentLinkOverABlocksLinkIsRefused(t *testing.T) {
	f := newFixture(t)
	a, b := f.titled("A"), f.titled("B")
	f.link(a, "blocks", b)
	wantMessage(t, f.cycleRefusal(a, "parent-of", b),
		a+" an ancestor of "+b, "so "+b+" could never start", "dependency loop")
	wantMessage(t, f.cycleRefusal(b, "parent-of", a),
		a+" a descendant of "+b, "a parent's open children already hold it", "dependency loop")

	// Further apart: Top blocks Leaf, and Mid, already over Leaf, goes under Top.
	top, mid, leaf := f.titled("Top"), f.titled("Mid"), f.titled("Leaf")
	f.link(mid, "parent-of", leaf)
	f.link(top, "blocks", leaf)
	wantMessage(t, f.cycleRefusal(top, "parent-of", mid), top+" an ancestor of "+leaf)
	if got := f.get(mid).Parent; got != nil {
		t.Errorf("mid parent = %s; want none", *got)
	}
}

func TestACycleHintPointsAtShowTree(t *testing.T) {
	f := newFixture(t)
	x, y := f.titled("X"), f.titled("Y")
	f.link(x, "blocks", y)
	refusal := f.cycleRefusal(y, "blocks", x)
	if want := "tess show " + y + " --tree"; !strings.Contains(refusal.Hint, want) {
		t.Errorf("hint %q; want it to name `%s`", refusal.Hint, want)
	}

	p, c := f.titled("P"), f.titled("C")
	f.link(p, "parent-of", c)
	for _, refusal := range []*tracker.Error{f.cycleRefusal(c, "blocks", p), f.cycleRefusal(p, "blocks", c)} {
		if want := "tess show " + p + " --tree"; !strings.Contains(refusal.Hint, want) {
			t.Errorf("hint %q; want it to name `%s`", refusal.Hint, want)
		}
	}
}

// Create makes the same links, so it is held to the same rule: a new issue
// can't be blocked by the parent it is created under, or by one above that.
func TestCreateRefusesABlockerThatIsAnAncestor(t *testing.T) {
	f := newFixture(t)
	top, mid, other := f.titled("Top"), f.titled("Mid"), f.titled("Other")
	f.link(top, "parent-of", mid)
	for _, blocker := range []string{mid, top} {
		_, err := f.tr.Create(f.ctx, tracker.NewIssue{Title: "Leaf", Parent: mid, BlockedBy: []string{other, blocker}})
		var refusal *tracker.Error
		if !errors.As(err, &refusal) || refusal.Code != tracker.CodeCycle {
			t.Fatalf("Create under %s blocked by %s = %v; want a cycle refusal", mid, blocker, err)
		}
		wantMessage(t, refusal, blocker+" is an ancestor of the new issue", "could never start", "dependency loop")
	}
	if got := f.list(tracker.Filter{}); len(got) != 3 {
		t.Errorf("issues after the refused creates = %v; want the three made before", got)
	}

	// A blocker elsewhere is fine, as is a sibling.
	sibling := f.create(tracker.NewIssue{Title: "Sibling", Parent: mid}).ID
	leaf := f.create(tracker.NewIssue{Title: "Leaf", Parent: mid, BlockedBy: []string{other, sibling}})
	if leaf.Parent == nil || *leaf.Parent != mid {
		t.Errorf("leaf parent = %v; want %s", leaf.Parent, mid)
	}
}
