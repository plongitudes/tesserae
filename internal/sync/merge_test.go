package sync

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/plongitudes/tesserae/internal/tracker"
)

// --- building snapshots in memory -------------------------------------------

// day is a time on 2026-01-01, n seconds after midnight: the tests order
// edits by small numbers.
func day(n int) time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Second)
}

// base is a plain issue created by "tester" at second 0.
func base(id string) *Issue {
	return &Issue{
		ID: id, Title: "Title of " + id,
		Description: emptyHash, Design: emptyHash, AcceptanceCriteria: emptyHash, Notes: emptyHash,
		Type: "task", Priority: 2, Status: "ready",
		Labels: []string{}, BlockedBy: []string{}, Related: []string{}, DiscoveredFrom: []string{},
		CreatedAt: day(0), CreatedBy: "tester", UpdatedAt: day(0),
	}
}

func snapOf(issues ...*Issue) *Snapshot {
	s := &Snapshot{
		Config: Config{Format: Format, Prefix: "t"},
		Issues: map[string]*Issue{}, Comments: map[string]*Comment{}, Events: map[string]*Event{}, Texts: map[string]string{},
	}
	for _, i := range issues {
		s.Issues[i.ID] = i
	}
	return s
}

// fork is a deep copy, through the canonical files.
func fork(t *testing.T, s *Snapshot) *Snapshot {
	t.Helper()
	out, err := FromFiles(s.Files())
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	return out
}

// edit changes an issue on one copy the way the tracker would: apply the
// change, move updated_at, and log one event at second n. The event's ID
// sorts by time, as a ULID does, with the copy's name after it.
func edit(s *Snapshot, copy, id string, n int, attr string, old, new any, change func(*Issue)) {
	i := s.Issues[id]
	change(i)
	i.UpdatedAt = day(n)
	logEvent(s, copy, id, n, attr, old, new)
}

func logEvent(s *Snapshot, copy, id string, n int, attr string, old, new any) *Event {
	e := &Event{
		ID: fmt.Sprintf("ev%04d%s%d", n, copy, len(s.Events)), IssueID: id,
		ChangedAt: day(n), ChangedBy: copy, Attr: attr, OldValue: old, NewValue: new,
	}
	s.Events[e.ID] = e
	return e
}

func setTitle(s *Snapshot, copy, id string, n int, title string) {
	old := s.Issues[id].Title
	edit(s, copy, id, n, "title", old, title, func(i *Issue) { i.Title = title })
}

// addTo and removeFrom change one element of a collection held on the issue.
func addTo(s *Snapshot, copy, id string, n int, attr, element string) {
	edit(s, copy, id, n, attr, nil, element, func(i *Issue) {
		list := collectionOf(i, attr)
		*list = append(*list, element)
	})
}

func removeFrom(s *Snapshot, copy, id string, n int, attr, element string) {
	edit(s, copy, id, n, attr, element, nil, func(i *Issue) {
		list := collectionOf(i, attr)
		*list = without(*list, element)
	})
}

func collectionOf(i *Issue, attr string) *[]string {
	switch attr {
	case "labels":
		return &i.Labels
	case "blocked_by":
		return &i.BlockedBy
	case "discovered_from":
		return &i.DiscoveredFrom
	case "related":
		return &i.Related
	}
	panic("no collection " + attr)
}

func without(list []string, e string) []string {
	out := []string{}
	for _, x := range list {
		if x != e {
			out = append(out, x)
		}
	}
	return out
}

func retire(s *Snapshot, copy, id string, n int) {
	edit(s, copy, id, n, "retired_at", nil, formatTime(day(n)), func(i *Issue) { i.RetiredAt = ptr(day(n)) })
}

func restore(s *Snapshot, copy, id string, n int) {
	old := formatTime(*s.Issues[id].RetiredAt)
	edit(s, copy, id, n, "retired_at", old, nil, func(i *Issue) { i.RetiredAt = nil })
}

// --- running the merge ------------------------------------------------------

// merge runs Merge both ways round and checks what must hold of every
// merge: it is symmetric (the same files, the same resolutions with won
// flipped), it leaves its inputs alone, and it adds no event. It returns the
// result as ours sees it.
func merge(t *testing.T, lkg, ours, theirs *Snapshot) (*Snapshot, []Resolution) {
	t.Helper()
	before := [][]byte{filesText(lkg), filesText(ours), filesText(theirs)}
	got, res, err := Merge(lkg, ours, theirs)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	back, resBack, err := Merge(lkg, theirs, ours)
	if err != nil {
		t.Fatalf("Merge the other way round: %v", err)
	}
	if a, b := filesText(got), filesText(back); !bytes.Equal(a, b) {
		t.Errorf("Merge is not symmetric.\nours, theirs:\n%s\ntheirs, ours:\n%s", a, b)
	}
	flipped := make([]Resolution, len(resBack))
	for i, r := range resBack {
		r.Won = map[string]string{"local": "remote", "remote": "local"}[r.Won]
		flipped[i] = r
	}
	if !reflect.DeepEqual(res, flipped) {
		t.Errorf("resolutions are not symmetric.\nours, theirs: %+v\ntheirs, ours (won flipped): %+v", res, flipped)
	}
	for i, s := range []*Snapshot{lkg, ours, theirs} {
		if !bytes.Equal(before[i], filesText(s)) {
			t.Errorf("Merge changed input %d", i)
		}
	}
	for id := range got.Events {
		if !hasEvent(id, lkg, ours, theirs) {
			t.Errorf("Merge added event %s", id)
		}
	}
	return got, res
}

func hasEvent(id string, snaps ...*Snapshot) bool {
	for _, s := range snaps {
		if s != nil && s.Events[id] != nil {
			return true
		}
	}
	return false
}

// filesText is a snapshot's files as one text, for comparing and printing.
func filesText(s *Snapshot) []byte {
	if s == nil {
		return nil
	}
	files := s.Files()
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var buf bytes.Buffer
	for _, p := range paths {
		fmt.Fprintf(&buf, "%s: %s", p, files[p])
		if !bytes.HasSuffix(files[p], []byte("\n")) {
			buf.WriteByte('\n')
		}
	}
	return buf.Bytes()
}

// wantResolutions compares everything about the resolutions but their
// times, which times_test.go covers.
func wantResolutions(t *testing.T, got []Resolution, want ...Resolution) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	got = slices.Clone(got)
	for i := range got {
		got[i].KeptAt, got[i].DroppedAt = time.Time{}, time.Time{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resolutions:\n got %+v\nwant %+v", got, want)
	}
}

func wantCode(t *testing.T, err error, code string) *tracker.Error {
	t.Helper()
	var e *tracker.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
	return e
}

// --- which issues -----------------------------------------------------------

func TestMergeKeepsAnIssueNewOnOneSide(t *testing.T) {
	ours, theirs := snapOf(base("t-aaa")), snapOf(base("t-bbb"))
	got, res := merge(t, nil, ours, theirs)
	if len(got.Issues) != 2 || got.Issues["t-aaa"] == nil || got.Issues["t-bbb"] == nil {
		t.Errorf("issues = %v, want t-aaa and t-bbb", got.Issues)
	}
	wantResolutions(t, res)
}

func TestMergeOfNothing(t *testing.T) {
	got, res, err := Merge(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Issues) != 0 || len(res) != 0 || got.Config.Format != Format {
		t.Errorf("got %+v, %v", got, res)
	}
}

func TestMergeTreatsAnIssueMissingFromASideAsUnchangedThere(t *testing.T) {
	lkg := snapOf(base("t-aaa"), base("t-bbb"))
	ours := fork(t, lkg)
	setTitle(ours, "a", "t-aaa", 5, "Edited")
	theirs := fork(t, lkg)
	delete(theirs.Issues, "t-aaa")
	delete(theirs.Issues, "t-bbb")
	delete(ours.Issues, "t-bbb")

	got, res := merge(t, lkg, ours, theirs)
	if title := got.Issues["t-aaa"].Title; title != "Edited" {
		t.Errorf("title = %q, want the edit", title)
	}
	if got.Issues["t-bbb"] == nil {
		t.Error("an issue of the LKG missing from both sides was dropped")
	}
	wantResolutions(t, res)
}

// Two copies that hold the same issue with no LKG (both imported one file,
// or one lost its LKG) merge it against an empty LKG record: nothing is in
// the LKG, so every event counts as new.
func TestMergeSameIssueTwiceWithoutAnLKG(t *testing.T) {
	start := snapOf(base("t-aaa"))
	ours, theirs := fork(t, start), fork(t, start)
	setTitle(theirs, "b", "t-aaa", 5, "From b")
	edit(ours, "a", "t-aaa", 6, "priority", float64(2), float64(0), func(i *Issue) { i.Priority = 0 })

	got, res := merge(t, nil, ours, theirs)
	i := got.Issues["t-aaa"]
	if i.Title != "From b" || i.Priority != 0 {
		t.Errorf("title %q priority %d, want From b and 0", i.Title, i.Priority)
	}
	wantResolutions(t, res)
}

func collision() (ours, theirs *Snapshot) {
	x, y := base("t-aaa"), base("t-aaa")
	x.Title, x.CreatedBy = "X on a", "a"
	y.Title, y.CreatedBy, y.CreatedAt = "Y on b", "b", day(1)
	ours, theirs = snapOf(x), snapOf(y)
	logEvent(ours, "a", "t-aaa", 2, "priority", float64(1), float64(2))
	logEvent(theirs, "b", "t-aaa", 3, "priority", float64(3), float64(2))
	ours.Comments["c-a"] = &Comment{ID: "c-a", IssueID: "t-aaa", Author: "a", Text: "on X", CreatedAt: day(2)}
	theirs.Comments["c-b"] = &Comment{ID: "c-b", IssueID: "t-aaa", Author: "b", Text: "on Y", CreatedAt: day(3)}
	return ours, theirs
}

func TestMergeRefusesTwoDifferentIssuesWithOneID(t *testing.T) {
	ours, theirs := collision()
	_, _, err := Merge(nil, ours, theirs)
	e := wantCode(t, err, "conflict")
	for _, want := range []string{"t-aaa", "X on a", "Y on b"} {
		if !strings.Contains(e.Message, want) {
			t.Errorf("message %q does not name %q", e.Message, want)
		}
	}
	if e.Hint == "" {
		t.Error("no hint")
	}
	_, _, err = Merge(nil, theirs, ours)
	wantCode(t, err, "conflict")

	// The same created_at by another creator is a collision too.
	theirs.Issues["t-aaa"].CreatedAt = ours.Issues["t-aaa"].CreatedAt
	_, _, err = Merge(nil, ours, theirs)
	wantCode(t, err, "conflict")
}

func TestMergeTakesTheLiveIssueWholeOverARetiredCollider(t *testing.T) {
	ours, theirs := collision()
	theirs.Issues["t-aaa"].RetiredAt = ptr(day(9))
	theirs.Issues["t-aaa"].UpdatedAt = day(9)
	theirs.Issues["t-aaa"].Labels = []string{"gone"}

	got, res := merge(t, nil, ours, theirs)
	if !bytes.Equal(got.Files()["issues/t-aaa.json"], ours.Files()["issues/t-aaa.json"]) {
		t.Errorf("issue = %s, want the live one whole", got.Files()["issues/t-aaa.json"])
	}
	if len(got.Events) != 1 || len(got.Comments) != 1 || got.Comments["c-a"] == nil {
		t.Errorf("events %v comments %v, want only the live issue's", got.Events, got.Comments)
	}
	wantResolutions(t, res)
}

func TestMergeKeepsTheEarlierCreatedOfTwoRetiredColliders(t *testing.T) {
	ours, theirs := collision()
	ours.Issues["t-aaa"].RetiredAt = ptr(day(9))
	theirs.Issues["t-aaa"].RetiredAt = ptr(day(8))

	got, _ := merge(t, nil, ours, theirs)
	if title := got.Issues["t-aaa"].Title; title != "X on a" {
		t.Errorf("title = %q, want the earlier-created X", title)
	}
	if got.Comments["c-b"] != nil || got.Comments["c-a"] == nil {
		t.Errorf("comments = %v, want the kept record's only", got.Comments)
	}
}

// --- config -----------------------------------------------------------------

func TestMergeRefusesTwoPrefixes(t *testing.T) {
	ours, theirs := snapOf(), snapOf()
	theirs.Config.Prefix = "other"
	_, _, err := Merge(nil, ours, theirs)
	e := wantCode(t, err, "prefix_mismatch")
	if !strings.Contains(e.Message, `"t"`) || !strings.Contains(e.Message, `"other"`) {
		t.Errorf("message %q does not name both prefixes", e.Message)
	}
}

func TestMergeRefusesAFormatItDoesNotKnow(t *testing.T) {
	ours, theirs := snapOf(), snapOf()
	theirs.Config.Format = Format + 1
	_, _, err := Merge(nil, ours, theirs)
	if e := wantCode(t, err, CodeFormatUnsupported); e.Hint == "" {
		t.Error("no hint to upgrade")
	}
	_, _, err = Merge(theirs, ours, nil)
	wantCode(t, err, CodeFormatUnsupported)
}

// --- the rule for a unit ----------------------------------------------------

// Every scalar unit: an edit on one side and none on the other arrives, and
// is not reported.
func TestMergeTakesAnEditMadeOnOneSide(t *testing.T) {
	edits := map[string]func(s *Snapshot){
		"title": func(s *Snapshot) { setTitle(s, "b", "t-aaa", 5, "Edited") },
		"description": func(s *Snapshot) {
			edit(s, "b", "t-aaa", 5, "description", emptyHash, hashOf("d"), func(i *Issue) { i.Description = hashOf("d") })
		},
		"design": func(s *Snapshot) {
			edit(s, "b", "t-aaa", 5, "design", emptyHash, hashOf("d"), func(i *Issue) { i.Design = hashOf("d") })
		},
		"acceptance_criteria": func(s *Snapshot) {
			edit(s, "b", "t-aaa", 5, "acceptance_criteria", emptyHash, hashOf("d"), func(i *Issue) { i.AcceptanceCriteria = hashOf("d") })
		},
		"notes": func(s *Snapshot) {
			edit(s, "b", "t-aaa", 5, "notes", emptyHash, hashOf("d"), func(i *Issue) { i.Notes = hashOf("d") })
		},
		"type": func(s *Snapshot) {
			edit(s, "b", "t-aaa", 5, "type", "task", "bug", func(i *Issue) { i.Type = "bug" })
		},
		"priority": func(s *Snapshot) {
			edit(s, "b", "t-aaa", 5, "priority", float64(2), float64(0), func(i *Issue) { i.Priority = 0 })
		},
		"status": func(s *Snapshot) {
			edit(s, "b", "t-aaa", 5, "status", "ready", "done", func(i *Issue) { i.Status = "done" })
		},
		"status_reason": func(s *Snapshot) {
			edit(s, "b", "t-aaa", 5, "status_reason", nil, "why", func(i *Issue) { i.StatusReason = ptr("why") })
		},
		"assignee": func(s *Snapshot) {
			edit(s, "b", "t-aaa", 5, "assignee", nil, "bo", func(i *Issue) { i.Assignee = ptr("bo") })
		},
		"owner": func(s *Snapshot) {
			edit(s, "b", "t-aaa", 5, "owner", nil, "bo", func(i *Issue) { i.Owner = ptr("bo") })
		},
		"due_at": func(s *Snapshot) {
			edit(s, "b", "t-aaa", 5, "due_at", nil, formatTime(day(99)), func(i *Issue) { i.DueAt = ptr(day(99)) })
		},
		"parent": func(s *Snapshot) {
			edit(s, "b", "t-aaa", 5, "parent", nil, "t-bbb", func(i *Issue) { i.Parent = ptr("t-bbb") })
		},
		"retired_at": func(s *Snapshot) { retire(s, "b", "t-aaa", 5) },
		"labels":     func(s *Snapshot) { addTo(s, "b", "t-aaa", 5, "labels", "ui") },
		"blocked_by": func(s *Snapshot) { addTo(s, "b", "t-aaa", 5, "blocked_by", "t-bbb") },
		"discovered_from": func(s *Snapshot) {
			addTo(s, "b", "t-aaa", 5, "discovered_from", "t-bbb")
		},
		"related": func(s *Snapshot) { addTo(s, "b", "t-aaa", 5, "related", "t-bbb") },
	}
	for name, change := range edits {
		t.Run(name, func(t *testing.T) {
			lkg := snapOf(base("t-aaa"), base("t-bbb"))
			ours, theirs := fork(t, lkg), fork(t, lkg)
			change(theirs)
			if bytes.Equal(filesText(theirs), filesText(lkg)) {
				t.Fatal("the edit changed nothing")
			}
			got, res := merge(t, lkg, ours, theirs)
			if a, b := filesText(got), filesText(theirs); !bytes.Equal(a, b) {
				t.Errorf("merged:\n%s\nwant the edited side:\n%s", a, b)
			}
			wantResolutions(t, res)
		})
	}
}

func TestMergeEditsToDifferentUnitsBothStand(t *testing.T) {
	lkg := snapOf(base("t-aaa"))
	ours, theirs := fork(t, lkg), fork(t, lkg)
	setTitle(ours, "a", "t-aaa", 7, "From a")
	edit(theirs, "b", "t-aaa", 5, "priority", float64(2), float64(0), func(i *Issue) { i.Priority = 0 })
	addTo(theirs, "b", "t-aaa", 6, "labels", "ui")

	got, res := merge(t, lkg, ours, theirs)
	i := got.Issues["t-aaa"]
	if i.Title != "From a" || i.Priority != 0 || !reflect.DeepEqual(i.Labels, []string{"ui"}) {
		t.Errorf("title %q priority %d labels %v", i.Title, i.Priority, i.Labels)
	}
	if !i.UpdatedAt.Equal(day(7)) {
		t.Errorf("updated_at = %s, want the later of the two sides", i.UpdatedAt)
	}
	if !i.CreatedAt.Equal(day(0)) || i.CreatedBy != "tester" {
		t.Errorf("created %s by %s changed", i.CreatedAt, i.CreatedBy)
	}
	wantResolutions(t, res)
}

func TestMergeLaterEventWinsAConflict(t *testing.T) {
	lkg := snapOf(base("t-aaa"))
	ours, theirs := fork(t, lkg), fork(t, lkg)
	setTitle(ours, "a", "t-aaa", 5, "From a")
	setTitle(theirs, "b", "t-aaa", 7, "From b")
	// A later edit to another field on ours does not decide the title.
	edit(ours, "a", "t-aaa", 9, "priority", float64(2), float64(1), func(i *Issue) { i.Priority = 1 })

	got, res := merge(t, lkg, ours, theirs)
	if title := got.Issues["t-aaa"].Title; title != "From b" {
		t.Errorf("title = %q, want the later edit", title)
	}
	if at := got.Issues["t-aaa"].UpdatedAt; !at.Equal(day(9)) {
		t.Errorf("updated_at = %s, want second 9", at)
	}
	wantResolutions(t, res, Resolution{
		Issue: "t-aaa", Field: "title", Kept: "From b", Dropped: "From a", Won: "remote", Reason: "later",
	})
	if len(got.Events) != 3 {
		t.Errorf("%d events, want both title edits and the priority edit", len(got.Events))
	}
}

func TestMergeBreaksATieInTimeByEventID(t *testing.T) {
	lkg := snapOf(base("t-aaa"))
	ours, theirs := fork(t, lkg), fork(t, lkg)
	setTitle(ours, "a", "t-aaa", 5, "From a")   // event ev0005a0
	setTitle(theirs, "b", "t-aaa", 5, "From b") // event ev0005b0, the larger ID
	got, res := merge(t, lkg, ours, theirs)
	if title := got.Issues["t-aaa"].Title; title != "From b" {
		t.Errorf("title = %q, want the edit with the larger event ID", title)
	}
	wantResolutions(t, res, Resolution{
		Issue: "t-aaa", Field: "title", Kept: "From b", Dropped: "From a", Won: "remote", Reason: "later",
	})
}

// No shortcut on "equal to the LKG": the copy that changed and changed back
// has the later event and wins.
func TestMergeChangeAndChangeBackWins(t *testing.T) {
	lkg := snapOf(base("t-aaa"))
	ours, theirs := fork(t, lkg), fork(t, lkg)
	setTitle(theirs, "b", "t-aaa", 5, "Edited on b")
	setTitle(ours, "a", "t-aaa", 6, "Edited on a")
	setTitle(ours, "a", "t-aaa", 7, "Title of t-aaa")

	got, res := merge(t, lkg, ours, theirs)
	if title := got.Issues["t-aaa"].Title; title != "Title of t-aaa" {
		t.Errorf("title = %q, want the changed-back one", title)
	}
	wantResolutions(t, res, Resolution{
		Issue: "t-aaa", Field: "title", Kept: "Title of t-aaa", Dropped: "Edited on b", Won: "local", Reason: "later",
	})
}

func hold(s *Snapshot, copy, id string, n int, reason string) {
	old := s.Issues[id].Status
	edit(s, copy, id, n, "status", old, "held", func(i *Issue) { i.Status = "held" })
	edit(s, copy, id, n, "status_reason", nil, reason, func(i *Issue) { i.StatusReason = ptr(reason) })
}

func TestMergeStatusAndItsReasonAreOneUnit(t *testing.T) {
	lkg := snapOf(base("t-aaa"))
	ours, theirs := fork(t, lkg), fork(t, lkg)
	hold(ours, "a", "t-aaa", 5, "waiting on a")
	edit(theirs, "b", "t-aaa", 6, "status", "ready", "in-progress", func(i *Issue) { i.Status = "in-progress" })

	got, res := merge(t, lkg, ours, theirs)
	if i := got.Issues["t-aaa"]; i.Status != "in-progress" || i.StatusReason != nil {
		t.Errorf("status %s reason %v, want in-progress with no reason", i.Status, optional(i.StatusReason))
	}
	wantResolutions(t, res, Resolution{
		Issue: "t-aaa", Field: "status", Kept: "in-progress", Dropped: "held", Won: "remote", Reason: "later",
	})

	// An event on the reason alone is an event for the unit.
	edit(ours, "a", "t-aaa", 7, "status_reason", "waiting on a", "still waiting", func(i *Issue) { i.StatusReason = ptr("still waiting") })
	got, res = merge(t, lkg, ours, theirs)
	if i := got.Issues["t-aaa"]; i.Status != "held" || optional(i.StatusReason) != "still waiting" {
		t.Errorf("status %s reason %v, want held, still waiting", i.Status, optional(i.StatusReason))
	}
	wantResolutions(t, res, Resolution{
		Issue: "t-aaa", Field: "status", Kept: "held", Dropped: "in-progress", Won: "local", Reason: "later",
	})
}

// --- collections ------------------------------------------------------------

func TestMergeCollectionElementsAreUnits(t *testing.T) {
	for _, attr := range []string{"labels", "blocked_by", "discovered_from"} {
		t.Run(attr, func(t *testing.T) {
			start := snapOf(base("t-aaa"))
			addTo(start, "x", "t-aaa", 1, attr, "t-old")
			list := func(s *Snapshot) []string { return *collectionOf(s.Issues["t-aaa"], attr) }

			// A removal on one copy and another element added on the other:
			// the removal is not resurrected, and nothing conflicts.
			ours, theirs := fork(t, start), fork(t, start)
			removeFrom(ours, "a", "t-aaa", 5, attr, "t-old")
			addTo(theirs, "b", "t-aaa", 6, attr, "t-new")
			got, res := merge(t, start, ours, theirs)
			if !reflect.DeepEqual(list(got), []string{"t-new"}) {
				t.Errorf("%s = %v, want only t-new", attr, list(got))
			}
			wantResolutions(t, res)

			// Removed on both, added again on one: the later add wins.
			ours, theirs = fork(t, start), fork(t, start)
			removeFrom(ours, "a", "t-aaa", 5, attr, "t-old")
			removeFrom(theirs, "b", "t-aaa", 6, attr, "t-old")
			addTo(theirs, "b", "t-aaa", 7, attr, "t-old")
			got, res = merge(t, start, ours, theirs)
			if !reflect.DeepEqual(list(got), []string{"t-old"}) {
				t.Errorf("%s = %v, want t-old back", attr, list(got))
			}
			wantResolutions(t, res, Resolution{
				Issue: "t-aaa", Field: attr, Kept: "t-old", Dropped: nil, Won: "remote", Reason: "later",
			})

			// Added again on one, then removed later on the other: the removal wins.
			addTo(ours, "a", "t-aaa", 6, attr, "t-old")
			removeFrom(ours, "a", "t-aaa", 9, attr, "t-old")
			got, res = merge(t, start, ours, theirs)
			if len(list(got)) != 0 {
				t.Errorf("%s = %v, want it empty", attr, list(got))
			}
			wantResolutions(t, res, Resolution{
				Issue: "t-aaa", Field: attr, Kept: nil, Dropped: "t-old", Won: "local", Reason: "later",
			})

			// The same element added on both copies is one element.
			ours, theirs = fork(t, start), fork(t, start)
			addTo(ours, "a", "t-aaa", 5, attr, "t-both")
			addTo(theirs, "b", "t-aaa", 6, attr, "t-both")
			got, res = merge(t, start, ours, theirs)
			if !reflect.DeepEqual(list(got), []string{"t-both", "t-old"}) {
				t.Errorf("%s = %v, want t-both and t-old", attr, list(got))
			}
			wantResolutions(t, res)
		})
	}
}

// related is one link per pair, held on the smaller ID, with its events on
// whichever endpoint the link was given from.
func TestMergeRelatedIsAUnitPerPair(t *testing.T) {
	lkg := snapOf(base("t-aaa"), base("t-bbb"))
	relate := func(s *Snapshot, copy string, n int, loggedOn string) {
		other := map[string]string{"t-aaa": "t-bbb", "t-bbb": "t-aaa"}[loggedOn]
		s.Issues["t-aaa"].Related = []string{"t-bbb"}
		s.Issues[loggedOn].UpdatedAt = day(n)
		logEvent(s, copy, loggedOn, n, "related", nil, other)
	}
	unrelate := func(s *Snapshot, copy string, n int, loggedOn string) {
		other := map[string]string{"t-aaa": "t-bbb", "t-bbb": "t-aaa"}[loggedOn]
		s.Issues["t-aaa"].Related = []string{}
		s.Issues[loggedOn].UpdatedAt = day(n)
		logEvent(s, copy, loggedOn, n, "related", other, nil)
	}

	// The same link in opposite wordings.
	ours, theirs := fork(t, lkg), fork(t, lkg)
	relate(ours, "a", 5, "t-aaa")
	relate(theirs, "b", 6, "t-bbb")
	got, res := merge(t, lkg, ours, theirs)
	if !reflect.DeepEqual(got.Issues["t-aaa"].Related, []string{"t-bbb"}) || len(got.Issues["t-bbb"].Related) != 0 {
		t.Errorf("related = %v and %v, want the link once, on t-aaa", got.Issues["t-aaa"].Related, got.Issues["t-bbb"].Related)
	}
	wantResolutions(t, res)
	linked := got

	// A removal logged on the other endpoint propagates.
	ours, theirs = fork(t, linked), fork(t, linked)
	unrelate(theirs, "b", 8, "t-bbb")
	got, res = merge(t, linked, ours, theirs)
	if len(got.Issues["t-aaa"].Related) != 0 {
		t.Errorf("related = %v, want the link removed", got.Issues["t-aaa"].Related)
	}
	wantResolutions(t, res)

	// Removed on both and added again on one, each logged on a different
	// endpoint: the later add wins.
	unrelate(ours, "a", 7, "t-aaa")
	relate(ours, "a", 9, "t-aaa")
	got, res = merge(t, linked, ours, theirs)
	if !reflect.DeepEqual(got.Issues["t-aaa"].Related, []string{"t-bbb"}) {
		t.Errorf("related = %v, want the link back", got.Issues["t-aaa"].Related)
	}
	wantResolutions(t, res, Resolution{
		Issue: "t-aaa", Field: "related", Kept: "t-bbb", Dropped: nil, Won: "local", Reason: "later",
	})
}

// --- after a retire ---------------------------------------------------------

func TestMergeDropsAnEditMadeAfterTheOtherCopyRetired(t *testing.T) {
	lkg := snapOf(base("t-aaa"))
	ours, theirs := fork(t, lkg), fork(t, lkg)
	retire(ours, "a", "t-aaa", 5)
	setTitle(theirs, "b", "t-aaa", 6, "Too late")
	addTo(theirs, "b", "t-aaa", 7, "labels", "late")
	edit(theirs, "b", "t-aaa", 4, "priority", float64(2), float64(0), func(i *Issue) { i.Priority = 0 })
	theirs.Comments["c1"] = &Comment{ID: "c1", IssueID: "t-aaa", Author: "b", Text: "after the retire", CreatedAt: day(8)}

	got, res := merge(t, lkg, ours, theirs)
	i := got.Issues["t-aaa"]
	if i.RetiredAt == nil || !i.RetiredAt.Equal(day(5)) {
		t.Fatalf("retired_at = %v, want second 5", i.RetiredAt)
	}
	if i.Title != "Title of t-aaa" || len(i.Labels) != 0 {
		t.Errorf("title %q labels %v, want the edits after the retire dropped", i.Title, i.Labels)
	}
	if i.Priority != 0 {
		t.Errorf("priority = %d, want the edit made before the retire", i.Priority)
	}
	if got.Comments["c1"] == nil {
		t.Error("a comment made after the retire was dropped")
	}
	wantResolutions(t, res,
		Resolution{Issue: "t-aaa", Field: "title", Kept: "Title of t-aaa", Dropped: "Too late", Won: "local", Reason: "retired"},
		Resolution{Issue: "t-aaa", Field: "labels", Kept: nil, Dropped: "late", Won: "local", Reason: "retired"},
	)
}

func TestMergeRetireRuleReportsAUnitOnce(t *testing.T) {
	lkg := snapOf(base("t-aaa"))
	ours, theirs := fork(t, lkg), fork(t, lkg)
	setTitle(ours, "a", "t-aaa", 3, "From a")
	retire(ours, "a", "t-aaa", 5)
	setTitle(theirs, "b", "t-aaa", 6, "Too late")
	got, res := merge(t, lkg, ours, theirs)
	if title := got.Issues["t-aaa"].Title; title != "From a" {
		t.Errorf("title = %q, want the retiring side's", title)
	}
	wantResolutions(t, res,
		Resolution{Issue: "t-aaa", Field: "title", Kept: "From a", Dropped: "Too late", Won: "local", Reason: "retired"})

	// The other copy's edit made before the retire wins the ordinary way.
	theirs = fork(t, lkg)
	setTitle(theirs, "b", "t-aaa", 4, "In time")
	got, res = merge(t, lkg, ours, theirs)
	if title := got.Issues["t-aaa"].Title; title != "In time" {
		t.Errorf("title = %q, want the edit made before the retire", title)
	}
	wantResolutions(t, res,
		Resolution{Issue: "t-aaa", Field: "title", Kept: "In time", Dropped: "From a", Won: "remote", Reason: "later"})
}

func TestMergeARestoreLaterThanTheRetireWins(t *testing.T) {
	lkg := snapOf(base("t-aaa"))
	ours, theirs := fork(t, lkg), fork(t, lkg)
	retire(ours, "a", "t-aaa", 5)
	setTitle(theirs, "b", "t-aaa", 6, "Edited while retired elsewhere")
	restore(ours, "a", "t-aaa", 8)
	got, res := merge(t, lkg, ours, theirs)
	if i := got.Issues["t-aaa"]; i.RetiredAt != nil || i.Title != "Edited while retired elsewhere" {
		t.Errorf("retired_at %v title %q, want restored with the edit", i.RetiredAt, i.Title)
	}
	wantResolutions(t, res)

	// Retire against restore is a unit like any other: the later event.
	retired := fork(t, lkg)
	retire(retired, "x", "t-aaa", 1)
	ours, theirs = fork(t, retired), fork(t, retired)
	restore(ours, "a", "t-aaa", 5)
	restore(theirs, "b", "t-aaa", 6)
	retire(theirs, "b", "t-aaa", 7)
	got, res = merge(t, retired, ours, theirs)
	if at := got.Issues["t-aaa"].RetiredAt; at == nil || !at.Equal(day(7)) {
		t.Errorf("retired_at = %v, want the later retire", at)
	}
	wantResolutions(t, res, Resolution{
		Issue: "t-aaa", Field: "retired_at", Kept: "2026-01-01T00:00:07Z", Dropped: nil, Won: "remote", Reason: "later",
	})
}

// --- no events --------------------------------------------------------------

func TestMergeWithoutEventsFallsBack(t *testing.T) {
	lkg := snapOf(base("t-aaa"))
	retitle := func(s *Snapshot, title string, n int) {
		s.Issues["t-aaa"].Title, s.Issues["t-aaa"].UpdatedAt = title, day(n)
	}

	t.Run("the side that differs from the LKG", func(t *testing.T) {
		ours, theirs := fork(t, lkg), fork(t, lkg)
		retitle(theirs, "Changed", 3)
		ours.Issues["t-aaa"].UpdatedAt = day(9) // later, and still not the one that changed
		theirs.Issues["t-aaa"].Labels = []string{"added"}
		got, res := merge(t, lkg, ours, theirs)
		if i := got.Issues["t-aaa"]; i.Title != "Changed" || !reflect.DeepEqual(i.Labels, []string{"added"}) {
			t.Errorf("title %q labels %v, want the changed side's", i.Title, i.Labels)
		}
		wantResolutions(t, res)
	})
	t.Run("an element removed without an event", func(t *testing.T) {
		labelled := fork(t, lkg)
		labelled.Issues["t-aaa"].Labels = []string{"ui"}
		ours, theirs := fork(t, labelled), fork(t, labelled)
		theirs.Issues["t-aaa"].Labels = []string{}
		got, _ := merge(t, labelled, ours, theirs)
		if labels := got.Issues["t-aaa"].Labels; len(labels) != 0 {
			t.Errorf("labels = %v, want the removal", labels)
		}
	})
	t.Run("then the later updated_at", func(t *testing.T) {
		ours, theirs := fork(t, lkg), fork(t, lkg)
		retitle(ours, "Aaa", 4)
		retitle(theirs, "Zzz", 6)
		got, res := merge(t, lkg, ours, theirs)
		if title := got.Issues["t-aaa"].Title; title != "Zzz" {
			t.Errorf("title = %q, want the side updated later", title)
		}
		wantResolutions(t, res)
	})
	t.Run("then the smaller canonical JSON", func(t *testing.T) {
		ours, theirs := fork(t, lkg), fork(t, lkg)
		retitle(ours, "Zzz", 4)
		retitle(theirs, "Aaa", 4)
		ours.Issues["t-aaa"].Assignee = ptr("someone") // "someone" sorts before null
		ours.Issues["t-aaa"].Priority = 10             // 10 sorts before 9 as JSON text
		theirs.Issues["t-aaa"].Priority = 9
		got, res := merge(t, lkg, ours, theirs)
		i := got.Issues["t-aaa"]
		if i.Title != "Aaa" || optional(i.Assignee) != "someone" || i.Priority != 10 {
			t.Errorf("title %q assignee %v priority %d", i.Title, optional(i.Assignee), i.Priority)
		}
		wantResolutions(t, res)
	})
	t.Run("the same issue twice with no LKG", func(t *testing.T) {
		ours, theirs := fork(t, lkg), fork(t, lkg)
		retitle(ours, "Edited here", 4)
		retitle(theirs, "Edited there", 6)
		got, _ := merge(t, nil, ours, theirs)
		if title := got.Issues["t-aaa"].Title; title != "Edited there" {
			t.Errorf("title = %q, want the side updated later", title)
		}
	})
	t.Run("the same latest event on both sides", func(t *testing.T) {
		// A copy that lost its LKG holds the event the other side made and
		// still has another value: the event can't say which is newer.
		ours, theirs := fork(t, lkg), fork(t, lkg)
		setTitle(theirs, "b", "t-aaa", 5, "From b")
		for id, e := range theirs.Events {
			ours.Events[id] = e
		}
		retitle(ours, "By hand", 7)
		got, res := merge(t, nil, ours, theirs)
		if title := got.Issues["t-aaa"].Title; title != "By hand" {
			t.Errorf("title = %q, want the side updated later", title)
		}
		wantResolutions(t, res)
	})
}

// --- events, comments, texts ------------------------------------------------

func TestMergeUnionsEventsCommentsAndTexts(t *testing.T) {
	lkg := snapOf(base("t-aaa"))
	logEvent(lkg, "x", "t-aaa", 1, "priority", float64(1), float64(2))
	lkg.Comments["c0"] = &Comment{ID: "c0", IssueID: "t-aaa", Author: "x", Text: "old", CreatedAt: day(1)}
	lkg.Texts[emptyHash] = ""
	ours, theirs := fork(t, lkg), fork(t, lkg)
	setTitle(ours, "a", "t-aaa", 5, "From a")
	ours.Comments["c1"] = &Comment{ID: "c1", IssueID: "t-aaa", Author: "a", Text: "from a", CreatedAt: day(5)}
	ours.Texts[hashOf("a")] = "a"
	setTitle(theirs, "b", "t-aaa", 6, "From b")
	theirs.Comments["c2"] = &Comment{ID: "c2", IssueID: "t-aaa", Author: "b", Text: "from b", CreatedAt: day(6)}
	theirs.Texts[hashOf("b")] = "b"

	got, _ := merge(t, lkg, ours, theirs)
	if len(got.Events) != 3 {
		t.Errorf("%d events, want the LKG's and each side's", len(got.Events))
	}
	for _, id := range []string{"c0", "c1", "c2"} {
		if got.Comments[id] == nil {
			t.Errorf("comment %s is missing", id)
		}
	}
	if len(got.Texts) != 3 || got.Texts[hashOf("a")] != "a" || got.Texts[hashOf("b")] != "b" {
		t.Errorf("texts = %v, want all three", got.Texts)
	}
}

// Merging again what a merge produced changes nothing and reports nothing:
// the second sync of a settled pair is quiet.
func TestMergeIsStableOnItsOwnResult(t *testing.T) {
	lkg := snapOf(base("t-aaa"), base("t-bbb"))
	ours, theirs := fork(t, lkg), fork(t, lkg)
	setTitle(ours, "a", "t-aaa", 5, "From a")
	setTitle(theirs, "b", "t-aaa", 6, "From b")
	retire(ours, "a", "t-bbb", 5)
	setTitle(theirs, "b", "t-bbb", 7, "Too late")
	merged, res := merge(t, lkg, ours, theirs)
	if len(res) != 2 {
		t.Fatalf("resolutions = %+v, want two", res)
	}

	// The other copy now merges its own state with the pushed result.
	again, _ := merge(t, lkg, theirs, merged)
	if a, b := filesText(again), filesText(merged); !bytes.Equal(a, b) {
		t.Errorf("the other copy reaches:\n%s\nwant:\n%s", a, b)
	}
	settled, res := merge(t, merged, merged, merged)
	if a, b := filesText(settled), filesText(merged); !bytes.Equal(a, b) {
		t.Errorf("a settled merge changed something:\n%s", a)
	}
	wantResolutions(t, res)
}

// An event the LKG already has is not new, however late its time: only what
// a side did since the LKG counts.
func TestMergeIgnoresEventsTheLKGHas(t *testing.T) {
	lkg := snapOf(base("t-aaa"))
	setTitle(lkg, "x", "t-aaa", 9, "Title in the LKG")
	ours, theirs := fork(t, lkg), fork(t, lkg)
	setTitle(ours, "a", "t-aaa", 5, "From a") // a clock behind the LKG's
	got, res := merge(t, lkg, ours, theirs)
	if title := got.Issues["t-aaa"].Title; title != "From a" {
		t.Errorf("title = %q, want the only edit since the LKG", title)
	}
	wantResolutions(t, res)
}

// In the same second as the retire, the event ID orders the edit and the
// retire, as it orders any two events.
func TestMergeRetireRuleBreaksATieInTimeByEventID(t *testing.T) {
	lkg := snapOf(base("t-aaa"))
	ours, theirs := fork(t, lkg), fork(t, lkg)
	retire(ours, "a", "t-aaa", 5)               // event ev0005a0
	setTitle(theirs, "b", "t-aaa", 5, "Edited") // event ev0005b0: after the retire
	got, res := merge(t, lkg, ours, theirs)
	if title := got.Issues["t-aaa"].Title; title != "Title of t-aaa" {
		t.Errorf("title = %q, want the edit dropped", title)
	}
	wantResolutions(t, res,
		Resolution{Issue: "t-aaa", Field: "title", Kept: "Title of t-aaa", Dropped: "Edited", Won: "local", Reason: "retired"})

	ours, theirs = fork(t, lkg), fork(t, lkg)
	retire(ours, "b", "t-aaa", 5)               // event ev0005b0
	setTitle(theirs, "a", "t-aaa", 5, "Edited") // event ev0005a0: before the retire
	got, res = merge(t, lkg, ours, theirs)
	if title := got.Issues["t-aaa"].Title; title != "Edited" {
		t.Errorf("title = %q, want the edit to stand", title)
	}
	wantResolutions(t, res)
}
