package sync

import (
	"testing"
	"time"
)

// The times a resolution carries (acceptance/README.md, "Sync output"): the
// events that decided the unit on each side.

func wantTimes(t *testing.T, r Resolution, keptAt, droppedAt time.Time) {
	t.Helper()
	if !r.KeptAt.Equal(keptAt) || !r.DroppedAt.Equal(droppedAt) {
		t.Errorf("%s %s: kept at %v, dropped at %v; want %v and %v",
			r.Issue, r.Field, r.KeptAt, r.DroppedAt, keptAt, droppedAt)
	}
}

func TestALaterResolutionCarriesEachSidesEventTime(t *testing.T) {
	lkg := snapOf(base("t-aaa"))
	ours, theirs := fork(t, lkg), fork(t, lkg)
	setTitle(ours, "a", "t-aaa", 3, "From a")
	setTitle(theirs, "b", "t-aaa", 1, "First from b")
	setTitle(theirs, "b", "t-aaa", 7, "From b")

	_, res := merge(t, lkg, ours, theirs)
	if len(res) != 1 {
		t.Fatalf("resolutions = %+v, want one", res)
	}
	wantTimes(t, res[0], day(7), day(3))
}

func TestARetiredResolutionCarriesTheRetiresTimeAndTheDroppedEdits(t *testing.T) {
	lkg := snapOf(base("t-aaa"))
	ours, theirs := fork(t, lkg), fork(t, lkg)
	retire(ours, "a", "t-aaa", 5)
	setTitle(theirs, "b", "t-aaa", 6, "Too late")
	addTo(theirs, "b", "t-aaa", 8, "labels", "late")

	_, res := merge(t, lkg, ours, theirs)
	if len(res) != 2 {
		t.Fatalf("resolutions = %+v, want two", res)
	}
	wantTimes(t, res[0], day(5), day(6))
	wantTimes(t, res[1], day(5), day(8))
}

func TestACycleResolutionCarriesTheDroppedLinksTimeAndNoKeptTime(t *testing.T) {
	lkg := snapOf(base("t-x"), base("t-y"))
	ours, theirs := fork(t, lkg), fork(t, lkg)
	block(ours, "a", "t-x", "t-y", 1)
	block(theirs, "b", "t-y", "t-x", 2)

	_, res := merge(t, lkg, ours, theirs)
	if len(res) != 1 {
		t.Fatalf("resolutions = %+v, want one", res)
	}
	wantTimes(t, res[0], time.Time{}, day(2))
}

func TestACycleResolutionForALinkWithNoEventHasNoTimes(t *testing.T) {
	lkg := snapOf(base("t-x"), base("t-y"))
	ours, theirs := fork(t, lkg), fork(t, lkg)
	// Imports: the links are there and nothing logged them.
	ours.Issues["t-y"].BlockedBy = []string{"t-x"}
	ours.Issues["t-y"].UpdatedAt = day(1)
	theirs.Issues["t-x"].BlockedBy = []string{"t-y"}
	theirs.Issues["t-x"].UpdatedAt = day(2)

	_, res := merge(t, lkg, ours, theirs)
	if len(res) != 1 {
		t.Fatalf("resolutions = %+v, want one", res)
	}
	wantTimes(t, res[0], time.Time{}, time.Time{})
}
