package sync

import (
	"slices"
	"strings"
)

// ReasonCycle is the Resolution.Reason of a link dropped to keep the
// hierarchy rules (ADR-0022).
const ReasonCycle = "cycle"

// The two kinds of link the hierarchy rules are about, named as the unit
// that holds them.
const (
	fieldParent    = "parent"
	fieldBlockedBy = "blocked_by"
)

// A link is a parent or blocks link as the snapshot holds it: on the child,
// naming the parent, or on the blocked issue, naming the blocker.
type link struct{ issue, field, other string }

func (l link) in(i *Issue) bool {
	switch {
	case i == nil:
		return false
	case l.field == fieldParent:
		return i.Parent != nil && *i.Parent == l.other
	}
	return slices.Contains(i.BlockedBy, l.other)
}

func (l link) compare(o link) int {
	return strings.Compare(l.issue+"\x00"+l.field+"\x00"+l.other, o.issue+"\x00"+o.field+"\x00"+o.other)
}

// structure makes the merged issues satisfy the hierarchy rules a single
// copy enforces (ADR-0016, ADR-0022): while a rule is broken, the latest of
// the links that break it and are new since the LKG is dropped and reported.
// It runs over every issue, retired ones too.
func (m *merger) structure(out *Snapshot) {
	for {
		broken := violations(out)
		if len(broken) == 0 {
			return
		}
		candidates := slices.DeleteFunc(slices.Clone(broken), func(l link) bool { return l.in(m.lkg.Issues[l.issue]) })
		if len(candidates) == 0 {
			// Only an LKG that broke the rules itself gets here (one written by
			// hand). The result must still be clean, so every link is a candidate.
			candidates = broken
		}
		drop := slices.MaxFunc(candidates, func(a, b link) int { return m.linkOrder(out, a, b) })

		issue := out.Issues[drop.issue]
		if drop.field == fieldParent {
			issue.Parent = nil
		} else {
			setMember(&issue.BlockedBy, drop.other, false)
		}
		events, _ := m.linkEvents(drop)
		m.resolved = append(m.resolved, Resolution{
			Issue: drop.issue, Field: drop.field, Kept: nil, Dropped: drop.other,
			Won: []string{WonLocal, WonRemote}[1-m.linkSide(drop)], Reason: ReasonCycle,
			DroppedAt: eventTime(later(events[0], events[1])),
		})
	}
}

// linkEvents is each side's latest new event for the unit that holds a
// link, for the sides whose record has the link.
func (m *merger) linkEvents(l link) (events [2]*Event, has [2]bool) {
	for side := range events {
		if has[side] = l.in(m.held[l.issue][side]); !has[side] {
			continue
		}
		if l.field == fieldParent {
			events[side] = m.scalarEvent(side, l.issue, fieldParent)
		} else {
			events[side] = m.elementEvent(side, l.issue, fieldBlockedBy, l.other)
		}
	}
	return events, has
}

// linkOrder orders links by how late they were made: by the event that made
// them, a link with no event first; then, as the merge does for a unit with
// no event, by the issue's updated_at; then by the link itself, which is
// arbitrary but the same on every copy.
func (m *merger) linkOrder(out *Snapshot, a, b link) int {
	ea, _ := m.linkEvents(a)
	eb, _ := m.linkEvents(b)
	la, lb := later(ea[0], ea[1]), later(eb[0], eb[1])
	switch {
	case after(la, lb):
		return 1
	case after(lb, la):
		return -1
	}
	if c := out.Issues[a.issue].UpdatedAt.Compare(out.Issues[b.issue].UpdatedAt); c != 0 {
		return c
	}
	return a.compare(b)
}

// linkSide is the side a dropped link is taken from: the only side that has
// it, or, when both made it, the side that made it later. Two sides that
// hold it by the same event are not told apart, and it counts as theirs.
func (m *merger) linkSide(l link) int {
	events, has := m.linkEvents(l)
	switch {
	case has[0] != has[1]:
		return pick(has[0])
	case after(events[0], events[1]):
		return 0
	}
	return 1
}

// violations is every parent and blocks link that takes part in a breach of
// the hierarchy rules, in a fixed order: a link on a loop of parent and
// blocks links (a link to itself included), and a blocks link from an
// ancestor to its descendant together with the parent links between the
// two. A descendant that blocks its ancestor needs no rule of its own: the
// blocks link and the parent links between the two are a loop.
func violations(s *Snapshot) []link {
	ids := make([]string, 0, len(s.Issues))
	for id := range s.Issues {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	// From an issue to what holds it up: its parent and its blockers.
	holders := func(id string) []string {
		issue := s.Issues[id]
		if issue == nil {
			return nil
		}
		if issue.Parent == nil {
			return issue.BlockedBy
		}
		return append([]string{*issue.Parent}, issue.BlockedBy...)
	}
	loop := loops(ids, holders)

	// ancestors is an issue's parent, its parent's parent, and so on, nearest
	// first; it stops if the parent links themselves loop.
	ancestors := func(id string) []string {
		var chain []string
		for issue := s.Issues[id]; issue != nil && issue.Parent != nil; issue = s.Issues[*issue.Parent] {
			if *issue.Parent == id || slices.Contains(chain, *issue.Parent) {
				break
			}
			chain = append(chain, *issue.Parent)
		}
		return chain
	}

	set := map[link]bool{}
	// climb marks the parent links from an issue up to the ancestor at depth.
	climb := func(id string, chain []string, depth int) {
		for _, parent := range chain[:depth+1] {
			set[link{id, fieldParent, parent}] = true
			id = parent
		}
	}
	for _, id := range ids {
		issue := s.Issues[id]
		if issue.Parent != nil && loop[id] != 0 && loop[id] == loop[*issue.Parent] {
			set[link{id, fieldParent, *issue.Parent}] = true
		}
		if len(issue.BlockedBy) == 0 {
			continue
		}
		up := ancestors(id)
		for _, blocker := range issue.BlockedBy {
			blocks := link{id, fieldBlockedBy, blocker}
			if blocker == id || (loop[id] != 0 && loop[id] == loop[blocker]) {
				set[blocks] = true
			}
			if depth := slices.Index(up, blocker); depth >= 0 {
				set[blocks] = true
				climb(id, up, depth)
			}
		}
	}

	out := make([]link, 0, len(set))
	for l := range set {
		out = append(out, l)
	}
	slices.SortFunc(out, link.compare)
	return out
}

// loops numbers the groups of issues that can each reach every other in the
// group by following next (the strongly connected components with more than
// one issue, by Tarjan's algorithm). A link between two issues of one group
// lies on a loop. An issue in no group has no number.
func loops(ids []string, next func(string) []string) map[string]int {
	var (
		group   = map[string]int{}
		index   = map[string]int{}
		low     = map[string]int{}
		onStack = map[string]bool{}
		stack   []string
		groups  int
		visit   func(id string)
	)
	visit = func(id string) {
		index[id] = len(index) + 1
		low[id] = index[id]
		stack = append(stack, id)
		onStack[id] = true
		for _, n := range next(id) {
			if index[n] == 0 {
				visit(n)
				low[id] = min(low[id], low[n])
			} else if onStack[n] {
				low[id] = min(low[id], index[n])
			}
		}
		if low[id] != index[id] {
			return
		}
		at := slices.Index(stack, id)
		members := stack[at:]
		stack = stack[:at]
		if len(members) > 1 {
			groups++
		}
		for _, member := range members {
			onStack[member] = false
			if len(members) > 1 {
				group[member] = groups
			}
		}
	}
	for _, id := range ids {
		if index[id] == 0 {
			visit(id)
		}
	}
	return group
}
