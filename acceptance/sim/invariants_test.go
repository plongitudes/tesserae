package sim

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	tsync "github.com/plongitudes/tesserae/internal/sync"
)

// check runs every invariant of acceptance/README.md, "The simulator", on a
// settled world, and fails the test with the operation log on the first
// violation.
func (w *world) check() {
	w.t.Helper()
	snaps := make([]*tsync.Snapshot, len(w.copies))
	for i, c := range w.copies {
		snaps[i] = w.load(c)
	}
	w.checkConverged(snaps)
	w.checkUnion(snaps[0])
	w.checkLatestEventWins(snaps[0])
	w.checkStructure(snaps[0])
}

func (w *world) fail(format string, args ...any) {
	w.t.Helper()
	w.t.Fatalf("%s\n\noperations:\n%s", fmt.Sprintf(format, args...), strings.Join(w.log, "\n"))
}

// Every copy serialises to the same bytes, and the same copy serialises the
// same way twice.
func (w *world) checkConverged(snaps []*tsync.Snapshot) {
	w.t.Helper()
	first := snaps[0].Files()
	if again := w.load(w.copies[0]).Files(); !sameFiles(first, again) {
		w.fail("copy %s serialises differently twice", w.copies[0].name)
	}
	for i := 1; i < len(snaps); i++ {
		files := snaps[i].Files()
		for path, want := range first {
			got, ok := files[path]
			if !ok {
				w.fail("copy %s lacks %s, which copy %s has", w.copies[i].name, path, w.copies[0].name)
			}
			if !bytes.Equal(want, got) {
				w.fail("copy %s and copy %s differ at %s:\n%s\n---\n%s", w.copies[0].name, w.copies[i].name, path, want, got)
			}
		}
		for path := range files {
			if _, ok := first[path]; !ok {
				w.fail("copy %s has %s, which copy %s lacks", w.copies[i].name, path, w.copies[0].name)
			}
		}
	}
}

func sameFiles(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if !bytes.Equal(v, b[k]) {
			return false
		}
	}
	return true
}

// Every event and comment any copy made is in the settled snapshot, and
// the snapshot holds no event the simulator never saw.
func (w *world) checkUnion(snap *tsync.Snapshot) {
	w.t.Helper()
	for id, rec := range w.events {
		if _, ok := snap.Events[id]; !ok {
			w.fail("event %s (%s %s on %s) is missing after sync", id, rec.ev.Attr, jsonText(rec.ev.NewValue), rec.issue)
		}
	}
	for id, ev := range snap.Events {
		if _, ok := w.events[id]; !ok {
			w.fail("event %s (%s on %s) appeared without any operation making it", id, ev.Attr, ev.IssueID)
		}
	}
	for id := range w.comments {
		if _, ok := snap.Comments[id]; !ok {
			w.fail("comment %s is missing after sync", id)
		}
	}
}

// Every unit of every issue holds the value its latest event anywhere gave
// it, except units a resolution with reason retired or cycle named. Status
// and status_reason are one unit: the latest event of either sets both to
// what the copy that made it held. A collection resolution names the
// element on whichever side has it (a refused removal keeps the element
// and drops nothing), so both Kept and Dropped are excused.
func (w *world) checkLatestEventWins(snap *tsync.Snapshot) {
	w.t.Helper()
	dropped := map[string]bool{}
	for _, r := range w.resolved {
		if r.Reason != "retired" && r.Reason != "cycle" {
			continue
		}
		switch r.Field {
		case "labels", "blocked_by", "discovered_from":
			for _, e := range []any{r.Kept, r.Dropped} {
				if e != nil {
					dropped[r.Issue+"/"+r.Field+"/"+jsonText(e)] = true
				}
			}
		case "related":
			for _, e := range []any{r.Kept, r.Dropped} {
				if other, ok := e.(string); ok {
					dropped["related/"+pairKey(r.Issue, other)] = true
				}
			}
		case "status", "status_reason":
			dropped[r.Issue+"/status"] = true
			dropped[r.Issue+"/status_reason"] = true
		default:
			dropped[r.Issue+"/"+r.Field] = true
		}
	}

	// Events in time order, per issue, plus the related pairs globally.
	byIssue := map[string][]recordedEvent{}
	for _, rec := range w.events {
		byIssue[rec.issue] = append(byIssue[rec.issue], rec)
	}
	relatedLatest := map[string]recordedEvent{}
	for _, recs := range byIssue {
		sort.Slice(recs, func(i, j int) bool { return eventBefore(recs[i], recs[j]) })
		for _, rec := range recs {
			if rec.ev.Attr != "related" {
				continue
			}
			other, _ := rec.ev.NewValue.(string)
			if other == "" {
				other, _ = rec.ev.OldValue.(string)
			}
			key := pairKey(rec.issue, other)
			if prev, ok := relatedLatest[key]; !ok || eventBefore(prev, rec) {
				relatedLatest[key] = rec
			}
		}
	}

	for id, base := range w.bases {
		iss, ok := snap.Issues[id]
		if !ok {
			w.fail("issue %s is missing after sync", id)
		}
		want := unitSet{scalars: map[string]string{}, sets: map[string]map[string]bool{}}
		for k, v := range base.scalars {
			want.scalars[k] = v
		}
		for k, set := range base.sets {
			want.sets[k] = map[string]bool{}
			for e := range set {
				want.sets[k][e] = true
			}
		}
		for _, rec := range byIssue[id] {
			ev := rec.ev
			switch ev.Attr {
			case "labels", "blocked_by", "discovered_from":
				if ev.NewValue != nil {
					want.sets[ev.Attr][jsonValueString(ev.NewValue)] = true
				} else if ev.OldValue != nil {
					delete(want.sets[ev.Attr], jsonValueString(ev.OldValue))
				}
			case "related":
			case "status", "status_reason":
				want.scalars["status"], want.scalars["status_reason"] = rec.status, rec.reason
			default:
				want.scalars[ev.Attr] = jsonText(ev.NewValue)
			}
		}
		got := snapshotUnits(iss)
		for _, attr := range scalarAttrs {
			if dropped[id+"/"+attr] {
				continue
			}
			if got.scalars[attr] != want.scalars[attr] {
				w.fail("issue %s %s: latest event says %s, snapshot has %s", id, attr, want.scalars[attr], got.scalars[attr])
			}
		}
		for _, attr := range setAttrs {
			for e := range want.sets[attr] {
				if dropped[id+"/"+attr+"/"+jsonText(e)] {
					continue
				}
				if !got.sets[attr][e] {
					w.fail("issue %s %s: latest event added %s, snapshot lacks it", id, attr, e)
				}
			}
			for e := range got.sets[attr] {
				if dropped[id+"/"+attr+"/"+jsonText(e)] {
					continue
				}
				if !want.sets[attr][e] {
					w.fail("issue %s %s: snapshot has %s, which the latest event removed or nothing added", id, attr, e)
				}
			}
		}
	}

	// related: a pair is present iff its latest event anywhere added it.
	present := map[string]bool{}
	for id, iss := range snap.Issues {
		for _, other := range iss.Related {
			if id > other {
				w.fail("issue %s lists related %s, which belongs on the smaller ID", id, other)
			}
			present[pairKey(id, other)] = true
		}
	}
	for key, rec := range relatedLatest {
		if dropped["related/"+key] {
			continue
		}
		if want := rec.ev.NewValue != nil; want != present[key] {
			w.fail("related %s: latest event added=%v, snapshot present=%v", key, want, present[key])
		}
	}
	for key := range present {
		if _, ok := relatedLatest[key]; !ok && !dropped["related/"+key] {
			w.fail("related %s is present with no event adding it", key)
		}
	}
}

func jsonValueString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return jsonText(v)
}

func eventBefore(a, b recordedEvent) bool {
	if !a.ev.ChangedAt.Equal(b.ev.ChangedAt) {
		return a.ev.ChangedAt.Before(b.ev.ChangedAt)
	}
	return a.ev.ID < b.ev.ID
}

// The hierarchy rules of ADR-0016 hold on the result, over all issues
// including retired ones: no cycle through blocks and parent links, and no
// blocks link between an issue and its own ancestor or descendant.
func (w *world) checkStructure(snap *tsync.Snapshot) {
	w.t.Helper()
	parent := map[string]string{}
	edges := map[string][]string{} // from the issue held up to what holds it up
	for id, iss := range snap.Issues {
		if iss.Parent != nil {
			parent[id] = *iss.Parent
			edges[id] = append(edges[id], *iss.Parent)
		}
		edges[id] = append(edges[id], iss.BlockedBy...)
	}
	ancestors := func(id string) map[string]bool {
		seen := map[string]bool{}
		for p, ok := parent[id]; ok && !seen[p]; p, ok = parent[p] {
			seen[p] = true
		}
		return seen
	}
	for id, iss := range snap.Issues {
		up := ancestors(id)
		for _, b := range iss.BlockedBy {
			if up[b] {
				w.fail("issue %s is blocked by its ancestor %s", id, b)
			}
			if ancestors(b)[id] {
				w.fail("issue %s is blocked by its descendant %s", id, b)
			}
		}
	}
	const white, grey, black = 0, 1, 2
	colour := map[string]int{}
	var visit func(id string, path []string)
	visit = func(id string, path []string) {
		colour[id] = grey
		for _, next := range edges[id] {
			switch colour[next] {
			case grey:
				w.fail("dependency loop: %s -> %s", strings.Join(append(path, id), " -> "), next)
			case white:
				visit(next, append(path, id))
			}
		}
		colour[id] = black
	}
	ids := make([]string, 0, len(snap.Issues))
	for id := range snap.Issues {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if colour[id] == white {
			visit(id, nil)
		}
	}
}
