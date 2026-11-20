package sync

import (
	"reflect"
	"testing"
)

// Structure after the merge (sync.md "Structure", ADR-0022): each copy's
// links are ones a single copy allows, and together they are not.

// block makes blocker block id on one copy at second n: the link is held on
// the blocked issue.
func block(s *Snapshot, copy, blocker, id string, n int) {
	addTo(s, copy, id, n, "blocked_by", blocker)
}

func setParent(s *Snapshot, copy, id, parent string, n int) {
	edit(s, copy, id, n, "parent", nil, parent, func(i *Issue) { i.Parent = ptr(parent) })
}

func cycleDrop(issue, field, other, won string) Resolution {
	return Resolution{Issue: issue, Field: field, Kept: nil, Dropped: other, Won: won, Reason: ReasonCycle}
}

func wantBlockedBy(t *testing.T, s *Snapshot, id string, want ...string) {
	t.Helper()
	if got := s.Issues[id].BlockedBy; !reflect.DeepEqual(got, append([]string{}, want...)) {
		t.Errorf("%s blocked_by = %v, want %v", id, got, want)
	}
}

func wantParent(t *testing.T, s *Snapshot, id string, want any) {
	t.Helper()
	if got := optional(s.Issues[id].Parent); got != want {
		t.Errorf("%s parent = %v, want %v", id, got, want)
	}
}

func TestMergeDropsTheLaterOfTwoBlocksLinksThatCloseALoop(t *testing.T) {
	lkg := snapOf(base("t-x"), base("t-y"))
	ours, theirs := fork(t, lkg), fork(t, lkg)
	block(ours, "a", "t-x", "t-y", 1)   // x blocks y
	block(theirs, "b", "t-y", "t-x", 2) // y blocks x, later

	got, res := merge(t, lkg, ours, theirs)

	wantBlockedBy(t, got, "t-y", "t-x")
	wantBlockedBy(t, got, "t-x")
	wantResolutions(t, res, cycleDrop("t-x", "blocked_by", "t-y", WonLocal))
	// The dropped link's add event stays in the history (ADR-0022).
	if len(got.Events) != 2 {
		t.Errorf("merged events = %d, want both add events", len(got.Events))
	}
}

func TestMergeDropsTheLatestLinkOfALongerLoopThroughAParentLink(t *testing.T) {
	lkg := snapOf(base("t-x"), base("t-y"), base("t-z"))
	ours, theirs := fork(t, lkg), fork(t, lkg)
	block(ours, "a", "t-x", "t-y", 1)       // x blocks y
	setParent(theirs, "b", "t-z", "t-y", 2) // y is the parent of z
	block(ours, "a", "t-z", "t-x", 3)       // z blocks x: z waits on y, y on x, x on z

	got, res := merge(t, lkg, ours, theirs)

	wantBlockedBy(t, got, "t-y", "t-x")
	wantParent(t, got, "t-z", "t-y")
	wantBlockedBy(t, got, "t-x")
	wantResolutions(t, res, cycleDrop("t-x", "blocked_by", "t-z", WonRemote))
}

func TestMergeDropsAParentLinkMadeAfterTheChildBlockedTheParent(t *testing.T) {
	lkg := snapOf(base("t-c"), base("t-p"))
	ours, theirs := fork(t, lkg), fork(t, lkg)
	block(theirs, "b", "t-c", "t-p", 1)   // c blocks p
	setParent(ours, "a", "t-c", "t-p", 2) // p is the parent of c, later

	got, res := merge(t, lkg, ours, theirs)

	wantParent(t, got, "t-c", nil)
	wantBlockedBy(t, got, "t-p", "t-c")
	wantResolutions(t, res, cycleDrop("t-c", "parent", "t-p", WonRemote))
}

func TestMergeDropsABlocksLinkMadeAfterTheParentLinkItRunsAlong(t *testing.T) {
	lkg := snapOf(base("t-c"), base("t-p"))
	ours, theirs := fork(t, lkg), fork(t, lkg)
	setParent(ours, "a", "t-c", "t-p", 1) // p is the parent of c
	block(theirs, "b", "t-p", "t-c", 2)   // p blocks its child, which is no loop of links

	got, res := merge(t, lkg, ours, theirs)

	wantParent(t, got, "t-c", "t-p")
	wantBlockedBy(t, got, "t-c")
	wantResolutions(t, res, cycleDrop("t-c", "blocked_by", "t-p", WonLocal))
}

// family is a grandparent g with a child p, and an issue c that one copy
// will put under p.
func family() *Snapshot {
	p := base("t-p")
	p.Parent = ptr("t-g")
	return snapOf(base("t-c"), base("t-g"), p)
}

func TestMergeDropsABlocksLinkIntoAGrandchild(t *testing.T) {
	lkg := family()
	ours, theirs := fork(t, lkg), fork(t, lkg)
	setParent(ours, "a", "t-c", "t-p", 1) // c becomes g's grandchild
	block(theirs, "b", "t-g", "t-c", 2)   // g blocks c, later

	got, res := merge(t, lkg, ours, theirs)

	wantParent(t, got, "t-c", "t-p")
	wantParent(t, got, "t-p", "t-g")
	wantBlockedBy(t, got, "t-c")
	wantResolutions(t, res, cycleDrop("t-c", "blocked_by", "t-g", WonLocal))
}

func TestMergeKeepsALinkOfTheLKGAndDropsTheNewOneBelowIt(t *testing.T) {
	// The grandchild blocks the grandparent first; the parent link that makes
	// it a grandchild is the later one. p's own parent link is older than both
	// and in the LKG, so it is no candidate.
	lkg := family()
	ours, theirs := fork(t, lkg), fork(t, lkg)
	block(theirs, "b", "t-c", "t-g", 1)
	setParent(ours, "a", "t-c", "t-p", 2)

	got, res := merge(t, lkg, ours, theirs)

	wantParent(t, got, "t-c", nil)
	wantParent(t, got, "t-p", "t-g")
	wantBlockedBy(t, got, "t-g", "t-c")
	wantResolutions(t, res, cycleDrop("t-c", "parent", "t-p", WonRemote))
}

func TestMergeDropsAParentLinkThatPutsAnIssueUnderTheGrandparentBlockingIt(t *testing.T) {
	// No loop of links here: g blocks c, and c then goes under g's child. The
	// parent link is the later of the two new links between them.
	lkg := family()
	ours, theirs := fork(t, lkg), fork(t, lkg)
	block(theirs, "b", "t-g", "t-c", 1)
	setParent(ours, "a", "t-c", "t-p", 2)

	got, res := merge(t, lkg, ours, theirs)

	wantParent(t, got, "t-c", nil)
	wantParent(t, got, "t-p", "t-g")
	wantBlockedBy(t, got, "t-c", "t-g")
	wantResolutions(t, res, cycleDrop("t-c", "parent", "t-p", WonRemote))
}

func TestMergeNeverDropsALinkTheLKGHasWhileANewOneCanGo(t *testing.T) {
	// x blocks y in the LKG. One copy takes the link off and puts it back, so
	// its event is the latest of all; the other copy's new link is still the
	// one that goes.
	y := base("t-y")
	y.BlockedBy = []string{"t-x"}
	lkg := snapOf(base("t-x"), y)
	ours, theirs := fork(t, lkg), fork(t, lkg)
	block(theirs, "b", "t-y", "t-x", 1)
	removeFrom(ours, "a", "t-y", 2, "blocked_by", "t-x")
	block(ours, "a", "t-x", "t-y", 3)

	got, res := merge(t, lkg, ours, theirs)

	wantBlockedBy(t, got, "t-y", "t-x")
	wantBlockedBy(t, got, "t-x")
	wantResolutions(t, res, cycleDrop("t-x", "blocked_by", "t-y", WonLocal))
}

func TestMergeDropsLinksUntilNoRuleIsBroken(t *testing.T) {
	// Two loops through x. Dropping the latest link opens one; the other
	// needs a second round.
	lkg := snapOf(base("t-x"), base("t-y"), base("t-z"))
	ours, theirs := fork(t, lkg), fork(t, lkg)
	block(ours, "a", "t-x", "t-y", 1)
	block(ours, "a", "t-x", "t-z", 2)
	block(theirs, "b", "t-y", "t-x", 3)
	block(theirs, "b", "t-z", "t-x", 4)

	got, res := merge(t, lkg, ours, theirs)

	wantBlockedBy(t, got, "t-x")
	wantBlockedBy(t, got, "t-y", "t-x")
	wantBlockedBy(t, got, "t-z", "t-x")
	wantResolutions(t, res,
		cycleDrop("t-x", "blocked_by", "t-z", WonLocal),
		cycleDrop("t-x", "blocked_by", "t-y", WonLocal))
}

func TestMergeChecksTheStructureThroughARetiredIssue(t *testing.T) {
	// The loop would close when y is restored.
	y := base("t-y")
	y.RetiredAt = ptr(day(0))
	lkg := snapOf(base("t-x"), y)
	ours, theirs := fork(t, lkg), fork(t, lkg)
	block(ours, "a", "t-x", "t-y", 1)
	block(theirs, "b", "t-y", "t-x", 2)

	got, res := merge(t, lkg, ours, theirs)

	wantBlockedBy(t, got, "t-y", "t-x")
	wantBlockedBy(t, got, "t-x")
	wantResolutions(t, res, cycleDrop("t-x", "blocked_by", "t-y", WonLocal))
}

func TestMergeDropsTheLinkOfTheLaterUpdatedIssueWhenNoLinkHasAnEvent(t *testing.T) {
	lkg := snapOf(base("t-x"), base("t-y"))
	ours, theirs := fork(t, lkg), fork(t, lkg)
	ours.Issues["t-y"].BlockedBy, ours.Issues["t-y"].UpdatedAt = []string{"t-x"}, day(1)
	theirs.Issues["t-x"].BlockedBy, theirs.Issues["t-x"].UpdatedAt = []string{"t-y"}, day(2)

	got, res := merge(t, lkg, ours, theirs)

	wantBlockedBy(t, got, "t-y", "t-x")
	wantBlockedBy(t, got, "t-x")
	wantResolutions(t, res, cycleDrop("t-x", "blocked_by", "t-y", WonLocal))
}

func TestMergeLeavesAStructureASingleCopyAllows(t *testing.T) {
	// A chain of blocks links and a tree beside it, made on two copies.
	lkg := family()
	lkg.Issues["t-x"] = base("t-x")
	ours, theirs := fork(t, lkg), fork(t, lkg)
	setParent(ours, "a", "t-c", "t-p", 1)
	block(theirs, "b", "t-x", "t-c", 2)
	block(ours, "a", "t-x", "t-g", 3)

	got, res := merge(t, lkg, ours, theirs)

	wantParent(t, got, "t-c", "t-p")
	wantBlockedBy(t, got, "t-c", "t-x")
	wantBlockedBy(t, got, "t-g", "t-x")
	wantResolutions(t, res)
}

func TestMergeDropsTheUpperParentLinkWhenItIsTheLatest(t *testing.T) {
	// g blocks c, which is p's child; p then goes under g.
	c := base("t-c")
	c.Parent = ptr("t-p")
	lkg := snapOf(c, base("t-g"), base("t-p"))
	ours, theirs := fork(t, lkg), fork(t, lkg)
	block(theirs, "b", "t-g", "t-c", 1)
	setParent(ours, "a", "t-p", "t-g", 2)

	got, res := merge(t, lkg, ours, theirs)

	wantParent(t, got, "t-c", "t-p")
	wantParent(t, got, "t-p", nil)
	wantBlockedBy(t, got, "t-c", "t-g")
	wantResolutions(t, res, cycleDrop("t-p", "parent", "t-g", WonRemote))
}

func TestMergeCountsALinkBothCopiesMadeAsTheLaterCopys(t *testing.T) {
	// Both copies made z block x; with y between them that closes a loop. The
	// link is as late as its later event, and that copy is the one overruled.
	lkg := snapOf(base("t-x"), base("t-y"), base("t-z"))
	ours, theirs := fork(t, lkg), fork(t, lkg)
	block(ours, "a", "t-x", "t-y", 1)
	block(theirs, "b", "t-y", "t-z", 2)
	block(ours, "a", "t-z", "t-x", 3)
	block(theirs, "b", "t-z", "t-x", 4)

	got, res := merge(t, lkg, ours, theirs)

	wantBlockedBy(t, got, "t-x")
	wantBlockedBy(t, got, "t-y", "t-x")
	wantBlockedBy(t, got, "t-z", "t-y")
	wantResolutions(t, res, cycleDrop("t-x", "blocked_by", "t-z", WonLocal))
}

func TestMergeRepairsAStructureTheLKGAlreadyBroke(t *testing.T) {
	// Only a snapshot written by hand looks like this: no link is new, so the
	// rule has no candidate, and the result must still be one a copy allows.
	x, y := base("t-x"), base("t-y")
	x.BlockedBy, y.BlockedBy = []string{"t-y"}, []string{"t-x"}
	lkg := snapOf(x, y)
	ours, theirs := fork(t, lkg), fork(t, lkg)
	setTitle(ours, "a", "t-x", 1, "Edited")

	got, res, err := Merge(lkg, ours, theirs)
	if err != nil {
		t.Fatal(err)
	}
	back, _, err := Merge(lkg, theirs, ours)
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Issues["t-x"].BlockedBy)+len(got.Issues["t-y"].BlockedBy) != 1 {
		t.Errorf("blocked_by = %v and %v, want one link left", got.Issues["t-x"].BlockedBy, got.Issues["t-y"].BlockedBy)
	}
	if len(res) != 1 || res[0].Reason != ReasonCycle {
		t.Errorf("resolutions = %+v, want one with reason cycle", res)
	}
	if a, b := filesText(got), filesText(back); string(a) != string(b) {
		t.Errorf("not symmetric:\n%s\n%s", a, b)
	}
}
