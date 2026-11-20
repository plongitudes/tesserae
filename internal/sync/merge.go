package sync

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/plongitudes/tesserae/internal/tracker"
)

// Codes the merge fails with.
const (
	CodeConflict       = "conflict"        // two different issues with one ID (ADR-0023)
	CodePrefixMismatch = "prefix_mismatch" // the two sides are two trackers
)

// The values of Resolution.Won and Resolution.Reason.
const (
	WonLocal  = "local"
	WonRemote = "remote"

	ReasonLater   = "later"   // both sides changed the unit; the later event won
	ReasonRetired = "retired" // an edit made after the other side retired the issue
	// ReasonCycle, a link dropped to keep the hierarchy rules, is in structure.go.
)

// Merge is the pure 3-way merge of sync.md, "The merge": a function of the
// three snapshots and nothing else, symmetric in ours and theirs apart from
// Resolution.Won. A nil snapshot is an empty one. The inputs are not changed.
func Merge(lkg, ours, theirs *Snapshot) (*Snapshot, []Resolution, error) {
	config, err := mergeConfig(lkg, ours, theirs)
	if err != nil {
		return nil, nil, err
	}
	m := &merger{
		lkg:     orEmpty(lkg),
		sides:   [2]*Snapshot{orEmpty(ours), orEmpty(theirs)},
		held:    map[string][2]*Issue{},
		whole:   map[string]int{},
		retires: map[string]*retirement{},
		// Never nil: "resolved" is always an array in the JSON output.
		resolved: []Resolution{},
	}
	for s, side := range m.sides {
		m.news[s] = newEvents(m.lkg, side)
	}
	out := &Snapshot{
		Config: config,
		Issues: map[string]*Issue{}, Comments: map[string]*Comment{}, Events: map[string]*Event{}, Texts: map[string]string{},
	}

	ids := map[string]bool{}
	for _, s := range []*Snapshot{m.lkg, m.sides[0], m.sides[1]} {
		for id := range s.Issues {
			ids[id] = true
		}
	}
	// In ID order, so that the resolutions, and which collision is reported,
	// don't depend on map order.
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		issue, err := m.issue(id)
		if err != nil {
			return nil, nil, err
		}
		out.Issues[id] = issue
	}
	m.related(out)
	m.structure(out)
	m.records(out)
	for _, s := range []*Snapshot{m.lkg, m.sides[0], m.sides[1]} {
		for hash, body := range s.Texts {
			out.Texts[hash] = body
		}
	}
	sort.SliceStable(m.resolved, func(i, j int) bool { return m.resolved[i].Issue < m.resolved[j].Issue })
	return out, m.resolved, nil
}

type merger struct {
	lkg   *Snapshot
	sides [2]*Snapshot // ours, theirs
	// news is each side's new events, the ones the LKG does not have, by
	// issue.
	news [2]map[string]*unitEvents
	// held is the record each side holds for an issue once "which issues" is
	// settled: its own, the LKG's when the side lacks it, nil when the issue
	// is new on the other side.
	held map[string][2]*Issue
	// whole names the side whose record won an ID collision whole
	// (ADR-0023); the other side's events and comments for the ID are dropped.
	whole    map[string]int
	retires  map[string]*retirement
	resolved []Resolution
}

func orEmpty(s *Snapshot) *Snapshot {
	if s == nil {
		return &Snapshot{}
	}
	return s
}

// mergeConfig checks that every snapshot given is in the format this build
// knows and that the two sides are one tracker.
func mergeConfig(lkg, ours, theirs *Snapshot) (Config, error) {
	for _, s := range []*Snapshot{lkg, ours, theirs} {
		if s != nil && s.Config.Format != Format {
			return Config{}, formatUnsupported(s.Config.Format)
		}
	}
	if ours != nil && theirs != nil && ours.Config.Prefix != theirs.Config.Prefix {
		// The two prefixes in order, so that both copies read the same message.
		prefixes := sorted([]string{ours.Config.Prefix, theirs.Config.Prefix})
		return Config{}, &tracker.Error{
			Code:    CodePrefixMismatch,
			Message: fmt.Sprintf("the two copies are different trackers: their prefixes are %q and %q", prefixes[0], prefixes[1]),
			Hint:    "sync with a remote whose issues have this copy's prefix (`tess doctor` shows it)",
		}
	}
	for _, s := range []*Snapshot{ours, theirs, lkg} {
		if s != nil {
			return s.Config, nil
		}
	}
	return Config{Format: Format}, nil
}

// --- events -----------------------------------------------------------------

// unitEvents is one issue's latest new event on one side, per unit: by attr
// for a scalar, and by attr and element for a collection.
type unitEvents struct {
	scalar  map[string]*Event
	element map[string]map[string]*Event
}

var collectionAttrs = []string{"labels", "blocked_by", "related", "discovered_from"}

func newEvents(lkg, side *Snapshot) map[string]*unitEvents {
	index := map[string]*unitEvents{}
	for id, e := range side.Events {
		if _, known := lkg.Events[id]; known {
			continue
		}
		u := index[e.IssueID]
		if u == nil {
			u = &unitEvents{scalar: map[string]*Event{}, element: map[string]map[string]*Event{}}
			index[e.IssueID] = u
		}
		if !slices.Contains(collectionAttrs, e.Attr) {
			u.scalar[e.Attr] = later(u.scalar[e.Attr], e)
			continue
		}
		if u.element[e.Attr] == nil {
			u.element[e.Attr] = map[string]*Event{}
		}
		// An add has the element as its new value and a removal as its old.
		for _, v := range []any{e.OldValue, e.NewValue} {
			if element, ok := v.(string); ok {
				u.element[e.Attr][element] = later(u.element[e.Attr][element], e)
			}
		}
	}
	return index
}

// after reports whether event a is later than event b, by changed_at and
// then by event ID. No event is time zero: any event is after it.
func after(a, b *Event) bool {
	switch {
	case a == nil:
		return false
	case b == nil:
		return true
	case !a.ChangedAt.Equal(b.ChangedAt):
		return a.ChangedAt.After(b.ChangedAt)
	}
	return a.ID > b.ID
}

func later(a, b *Event) *Event {
	if after(b, a) {
		return b
	}
	return a
}

// scalarEvent is a side's latest new event on an issue under any of attrs.
func (m *merger) scalarEvent(side int, id string, attrs ...string) *Event {
	u := m.news[side][id]
	if u == nil {
		return nil
	}
	var latest *Event
	for _, attr := range attrs {
		latest = later(latest, u.scalar[attr])
	}
	return latest
}

// elementEvent is a side's latest new event on an issue that added or
// removed element in the collection attr.
func (m *merger) elementEvent(side int, id, attr, element string) *Event {
	u := m.news[side][id]
	if u == nil {
		return nil
	}
	return u.element[attr][element]
}

// --- the rule for a unit ----------------------------------------------------

// A choice is one unit on which the two sides differ.
type choice struct {
	issue, field string
	values       [2]any    // each side's value as a resolution reports it
	events       [2]*Event // each side's latest new event for the unit
	winner       int
	retired      *retirement    // the retire that put the winner aside, if one did
	apply        func(side int) // write that side's value into the result
}

// decide is the rule of ADR-0021: the side with the later new event; with
// none on either side (or the same one on both, which says nothing about
// which value is newer), the only side that differs from the LKG, then the
// later updated_at, then the smaller canonical JSON value.
func (c *choice) decide(differs [2]bool, updated [2]time.Time, canonical [2]string) {
	a, b := c.events[0], c.events[1]
	switch {
	case a == b || (a != nil && b != nil && a.ID == b.ID):
	case after(a, b):
		c.winner = 0
		return
	default:
		c.winner = 1
		return
	}
	switch {
	case differs[0] != differs[1]:
		c.winner = pick(differs[0])
	case !updated[0].Equal(updated[1]):
		c.winner = pick(updated[0].After(updated[1]))
	default:
		c.winner = pick(canonical[0] < canonical[1])
	}
}

// pick is side 0 when ours is true and side 1 otherwise.
func pick(ours bool) int {
	if ours {
		return 0
	}
	return 1
}

// A retirement is an issue that resolved to retired where the sides
// differed: the side that retired it and when.
type retirement struct {
	side int
	at   time.Time
	id   string // the retire event's ID; empty when the retire has no event
}

// refuse applies the retire rule to a choice: a unit the non-retiring side
// won with an event later than the retire goes back to the retiring side's
// value, as a copy would have refused the edit.
func (r *retirement) refuse(c *choice) {
	if r == nil || c.winner == r.side {
		return
	}
	e := c.events[c.winner]
	if e == nil {
		return
	}
	if e.ChangedAt.After(r.at) || (e.ChangedAt.Equal(r.at) && e.ID > r.id) {
		c.winner, c.retired = r.side, r
	}
}

// settle writes the winner's value and reports the choice if it is one a
// user should hear of: both sides changed the unit since the LKG, or the
// retire rule dropped an edit.
func (m *merger) settle(c *choice) {
	c.apply(c.winner)
	r := Resolution{
		Issue: c.issue, Field: c.field,
		Kept: c.values[c.winner], Dropped: c.values[1-c.winner],
		Won:    []string{WonLocal, WonRemote}[c.winner],
		KeptAt: eventTime(c.events[c.winner]), DroppedAt: eventTime(c.events[1-c.winner]),
	}
	switch a, b := c.events[0], c.events[1]; {
	case c.retired != nil:
		// What decided the unit on the kept side is the retire.
		r.Reason, r.KeptAt = ReasonRetired, c.retired.at
	case a != nil && b != nil && a.ID != b.ID:
		r.Reason = ReasonLater
	default:
		return
	}
	m.resolved = append(m.resolved, r)
}

// eventTime is when an event was made; the zero time for no event.
func eventTime(e *Event) time.Time {
	if e == nil {
		return time.Time{}
	}
	return e.ChangedAt
}

// --- units ------------------------------------------------------------------

// A unit is a scalar field, or two that change together, merged as one.
type unit struct {
	name  string
	attrs []string              // the event attrs that name it
	value func(*Issue) any      // the JSON value a resolution reports
	whole func(*Issue) any      // what is compared, when that is more than value
	copy  func(dst, src *Issue) // take the unit from src
}

func optional(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func optionalTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

var units = []unit{
	{name: "title", value: func(i *Issue) any { return i.Title }, copy: func(d, s *Issue) { d.Title = s.Title }},
	{name: "description", value: func(i *Issue) any { return i.Description }, copy: func(d, s *Issue) { d.Description = s.Description }},
	{name: "design", value: func(i *Issue) any { return i.Design }, copy: func(d, s *Issue) { d.Design = s.Design }},
	{name: "acceptance_criteria", value: func(i *Issue) any { return i.AcceptanceCriteria },
		copy: func(d, s *Issue) { d.AcceptanceCriteria = s.AcceptanceCriteria }},
	{name: "notes", value: func(i *Issue) any { return i.Notes }, copy: func(d, s *Issue) { d.Notes = s.Notes }},
	{name: "type", value: func(i *Issue) any { return i.Type }, copy: func(d, s *Issue) { d.Type = s.Type }},
	{name: "priority", value: func(i *Issue) any { return float64(i.Priority) }, copy: func(d, s *Issue) { d.Priority = s.Priority }},
	{name: "status", attrs: []string{"status", "status_reason"},
		value: func(i *Issue) any { return i.Status },
		whole: func(i *Issue) any { return []any{i.Status, optional(i.StatusReason)} },
		copy:  func(d, s *Issue) { d.Status, d.StatusReason = s.Status, s.StatusReason }},
	{name: "assignee", value: func(i *Issue) any { return optional(i.Assignee) }, copy: func(d, s *Issue) { d.Assignee = s.Assignee }},
	{name: "owner", value: func(i *Issue) any { return optional(i.Owner) }, copy: func(d, s *Issue) { d.Owner = s.Owner }},
	{name: "due_at", value: func(i *Issue) any { return optionalTime(i.DueAt) }, copy: func(d, s *Issue) { d.DueAt = s.DueAt }},
	{name: "parent", value: func(i *Issue) any { return optional(i.Parent) }, copy: func(d, s *Issue) { d.Parent = s.Parent }},
	{name: unitRetiredAt, value: func(i *Issue) any { return optionalTime(i.RetiredAt) }, copy: func(d, s *Issue) { d.RetiredAt = s.RetiredAt }},
}

const unitRetiredAt = "retired_at"

func (u unit) compared(i *Issue) string {
	if u.whole != nil {
		return canonical(u.whole(i))
	}
	return canonical(u.value(i))
}

// The collections held on one issue; related is a pair of issues and is
// merged in related.
var collections = []struct {
	name string
	of   func(*Issue) *[]string
}{
	{"labels", func(i *Issue) *[]string { return &i.Labels }},
	{"blocked_by", func(i *Issue) *[]string { return &i.BlockedBy }},
	{"discovered_from", func(i *Issue) *[]string { return &i.DiscoveredFrom }},
}

// canonical is a value's JSON as a snapshot writes it.
func canonical(v any) string {
	return strings.TrimSuffix(string(encode(v)), "\n")
}

// member is an element's value on a side: the element where the side has
// it, null where it does not.
func member(present bool, element string) any {
	if present {
		return element
	}
	return nil
}

func setMember(list *[]string, element string, present bool) {
	at := slices.Index(*list, element)
	switch {
	case present && at < 0:
		*list = append(*list, element)
	case !present && at >= 0:
		*list = slices.Delete(*list, at, at+1)
	}
}

// --- issues -----------------------------------------------------------------

// clone is a copy of an issue that shares no slice with it, without its
// related links, which the merge decides pair by pair.
func clone(i *Issue) *Issue {
	out := *i
	out.Labels, out.BlockedBy, out.DiscoveredFrom = sorted(i.Labels), sorted(i.BlockedBy), sorted(i.DiscoveredFrom)
	out.Related = []string{}
	return &out
}

// issue decides which record an ID stands for (sync.md, "Which issues") and
// merges it.
func (m *merger) issue(id string) (*Issue, error) {
	l, a, b := m.lkg.Issues[id], m.sides[0].Issues[id], m.sides[1].Issues[id]
	switch {
	case a == nil && b == nil:
		m.held[id] = [2]*Issue{l, l}
		return clone(l), nil
	case l == nil && a == nil:
		m.held[id] = [2]*Issue{nil, b}
		return clone(b), nil
	case l == nil && b == nil:
		m.held[id] = [2]*Issue{a, nil}
		return clone(a), nil
	case l == nil && !(a.CreatedAt.Equal(b.CreatedAt) && a.CreatedBy == b.CreatedBy):
		keep, err := collide(id, a, b)
		if err != nil {
			return nil, err
		}
		// From here on the other side holds the kept record too, and its own
		// record's events decide nothing.
		kept := [2]*Issue{a, b}[keep]
		m.whole[id] = keep
		m.held[id] = [2]*Issue{kept, kept}
		delete(m.news[1-keep], id)
		return clone(kept), nil
	case a == nil:
		a = l
	case b == nil:
		b = l
	}
	m.held[id] = [2]*Issue{a, b}
	return m.fields(id, l, a, b), nil
}

// collide settles two different issues with one ID (ADR-0023): a refusal,
// unless a retired record can give way. It returns the side kept.
func collide(id string, a, b *Issue) (int, error) {
	switch {
	case a.RetiredAt == nil && b.RetiredAt == nil:
		return 0, &tracker.Error{
			Code: CodeConflict,
			Message: fmt.Sprintf("%s is two different issues: %q on this copy and %q on the other",
				id, a.Title, b.Title),
			Hint: fmt.Sprintf("on one copy, create the issue again under a new ID (`tess show %s --json` has every field), "+
				"`tess retire %s`, and sync again", id, id),
		}
	case a.RetiredAt == nil:
		return 0, nil
	case b.RetiredAt == nil:
		return 1, nil
	case !a.CreatedAt.Equal(b.CreatedAt):
		return pick(a.CreatedAt.Before(b.CreatedAt)), nil
	}
	return pick(a.CreatedBy < b.CreatedBy), nil
}

// fields merges one issue's units from the two sides' records. l is nil
// when the issue is not in the LKG, and the LKG record is then an empty one.
func (m *merger) fields(id string, l, a, b *Issue) *Issue {
	sides := [2]*Issue{a, b}
	updated := [2]time.Time{a.UpdatedAt, b.UpdatedAt}
	out := clone(a)
	if l != nil {
		out.CreatedAt, out.CreatedBy = l.CreatedAt, l.CreatedBy
	} else {
		l = &Issue{}
	}
	if b.UpdatedAt.After(out.UpdatedAt) {
		out.UpdatedAt = b.UpdatedAt
	}

	var choices []*choice
	var retire *retirement
	for _, u := range units {
		canon := [2]string{u.compared(a), u.compared(b)}
		if canon[0] == canon[1] {
			continue
		}
		attrs := u.attrs
		if attrs == nil {
			attrs = []string{u.name}
		}
		c := &choice{
			issue: id, field: u.name,
			values: [2]any{u.value(a), u.value(b)},
			events: [2]*Event{m.scalarEvent(0, id, attrs...), m.scalarEvent(1, id, attrs...)},
			apply:  func(side int) { u.copy(out, sides[side]) },
		}
		was := u.compared(l)
		c.decide([2]bool{canon[0] != was, canon[1] != was}, updated, canon)
		choices = append(choices, c)
		if won := sides[c.winner].RetiredAt; u.name == unitRetiredAt && won != nil {
			retire = &retirement{side: c.winner, at: *won}
			if e := c.events[c.winner]; e != nil {
				retire.at, retire.id = e.ChangedAt, e.ID
			}
		}
	}
	for _, col := range collections {
		in := [2][]string{*col.of(a), *col.of(b)}
		was := *col.of(l)
		elements := sorted(append(slices.Clone(in[0]), in[1]...))
		for _, element := range slices.Compact(elements) {
			has := [2]bool{slices.Contains(in[0], element), slices.Contains(in[1], element)}
			if has[0] == has[1] {
				continue
			}
			c := &choice{
				issue: id, field: col.name,
				values: [2]any{member(has[0], element), member(has[1], element)},
				events: [2]*Event{m.elementEvent(0, id, col.name, element), m.elementEvent(1, id, col.name, element)},
				apply:  func(side int) { setMember(col.of(out), element, has[side]) },
			}
			had := slices.Contains(was, element)
			c.decide([2]bool{has[0] != had, has[1] != had}, updated, [2]string{canonical(c.values[0]), canonical(c.values[1])})
			choices = append(choices, c)
		}
	}

	if retire != nil {
		m.retires[id] = retire
	}
	for _, c := range choices {
		if c.field != unitRetiredAt {
			retire.refuse(c)
		}
		m.settle(c)
	}
	for _, col := range collections {
		slices.Sort(*col.of(out))
	}
	return out
}

// related merges the related links, each pair of issues one unit that
// lives on the smaller ID and whose events are on either endpoint.
func (m *merger) related(out *Snapshot) {
	type pair struct{ holder, other string }
	pairsOf := func(issues func(id string) *Issue) map[pair]bool {
		pairs := map[pair]bool{}
		for id := range out.Issues {
			issue := issues(id)
			if issue == nil {
				continue
			}
			for _, other := range issue.Related {
				p := pair{id, other}
				if other < id {
					p = pair{other, id}
				}
				pairs[p] = true
			}
		}
		return pairs
	}
	was := pairsOf(func(id string) *Issue { return m.lkg.Issues[id] })
	in := [2]map[pair]bool{
		pairsOf(func(id string) *Issue { return m.held[id][0] }),
		pairsOf(func(id string) *Issue { return m.held[id][1] }),
	}
	all := map[pair]bool{}
	for _, pairs := range in {
		for p := range pairs {
			all[p] = true
		}
	}
	ordered := make([]pair, 0, len(all))
	for p := range all {
		ordered = append(ordered, p)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].holder != ordered[j].holder {
			return ordered[i].holder < ordered[j].holder
		}
		return ordered[i].other < ordered[j].other
	})

	for _, p := range ordered {
		holder := out.Issues[p.holder]
		if holder == nil {
			continue // a link from an issue no snapshot has
		}
		has := [2]bool{in[0][p], in[1][p]}
		if has[0] == has[1] {
			holder.Related = append(holder.Related, p.other)
			continue
		}
		c := &choice{
			issue: p.holder, field: "related",
			values: [2]any{member(has[0], p.other), member(has[1], p.other)},
			apply:  func(side int) { setMember(&holder.Related, p.other, has[side]) },
		}
		var updated [2]time.Time
		for side := range c.events {
			c.events[side] = later(
				m.elementEvent(side, p.holder, "related", p.other),
				m.elementEvent(side, p.other, "related", p.holder))
			if held := m.held[p.holder][side]; held != nil {
				updated[side] = held.UpdatedAt
			}
		}
		c.decide([2]bool{has[0] != was[p], has[1] != was[p]}, updated, [2]string{canonical(c.values[0]), canonical(c.values[1])})
		m.retires[p.holder].refuse(c)
		m.settle(c)
	}
	for _, issue := range out.Issues {
		slices.Sort(issue.Related)
	}
}

// --- events and comments ----------------------------------------------------

// records fills in the events and comments: the union of the three
// snapshots, less those of a record that lost an ID collision.
func (m *merger) records(out *Snapshot) {
	for _, s := range []*Snapshot{m.lkg, m.sides[0], m.sides[1]} {
		for id, e := range s.Events {
			if keep, collided := m.whole[e.IssueID]; collided && m.sides[keep].Events[id] == nil {
				continue
			}
			out.Events[id] = smaller(out.Events[id], e, eventBytes)
		}
		for id, c := range s.Comments {
			if keep, collided := m.whole[c.IssueID]; collided && m.sides[keep].Comments[id] == nil {
				continue
			}
			out.Comments[id] = smaller(out.Comments[id], c, commentBytes)
		}
	}
}

// smaller chooses between two records with one ID. They are the same record
// unless a snapshot was written by hand; then the one whose file sorts first
// is taken, so that the choice does not depend on which side is ours.
func smaller[T any](a, b *T, file func(*T) []byte) *T {
	if a == nil {
		return b
	}
	if a == b || bytes.Compare(file(a), file(b)) <= 0 {
		return a
	}
	return b
}
