package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// graph builds a small issue graph in a :memory: store with plain SQL, the
// way store writes will, so the readiness views are tested on their own.
type graph struct {
	t *testing.T
	s *Store
}

func newGraph(t *testing.T) *graph {
	return &graph{t: t, s: openMemory(t)}
}

func (g *graph) exec(query string, args ...any) {
	g.t.Helper()
	ctx := context.Background()
	err := g.s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, query, args...)
		return err
	})
	if err != nil {
		g.t.Fatalf("%s %v: %v", query, args, err)
	}
}

// issue adds an issue with the given stored status.
func (g *graph) issue(id, status string) {
	g.t.Helper()
	g.exec(`INSERT INTO issues (id, title, status, created_at, created_by, updated_at)
	        VALUES (?, ?, ?, '2026-01-01T00:00:00Z', 'tester', '2026-01-01T00:00:00Z')`, id, id, status)
}

func (g *graph) setStatus(id, status string) {
	g.t.Helper()
	g.exec(`UPDATE issues SET status = ? WHERE id = ?`, status, id)
}

func (g *graph) retire(id string) {
	g.t.Helper()
	g.exec(`UPDATE issues SET retired_at = '2026-01-02T00:00:00Z' WHERE id = ?`, id)
}

// blocks records "blocker blocks blocked"; parent records "parent parent-of child".
func (g *graph) blocks(blocker, blocked string) { g.t.Helper(); g.link(blocker, "blocks", blocked) }
func (g *graph) parent(parent, child string)    { g.t.Helper(); g.link(parent, "parent", child) }

func (g *graph) link(from, typ, to string) {
	g.t.Helper()
	g.exec(`INSERT INTO links (from_id, type, to_id) VALUES (?, ?, ?)`, from, typ, to)
}

// ids runs a query returning one text column and gives the values back sorted
// by the query's own ORDER BY.
func (g *graph) ids(query string, args ...any) []string {
	g.t.Helper()
	ctx := context.Background()
	out := []string{}
	err := g.s.Read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			out = append(out, id)
		}
		return rows.Err()
	})
	if err != nil {
		g.t.Fatalf("%s: %v", query, err)
	}
	return out
}

func (g *graph) blocked() []string {
	g.t.Helper()
	return g.ids(`SELECT id FROM blocked_issues ORDER BY id`)
}

func (g *graph) wantBlocked(want ...string) {
	g.t.Helper()
	if want == nil {
		want = []string{}
	}
	if got := g.blocked(); !reflect.DeepEqual(got, want) {
		g.t.Fatalf("blocked issues = %v, want %v", got, want)
	}
}

// displayed returns the status an issue shows: the stored one, except that a
// stored ready shows as waiting when it's blocked or has open children.
func (g *graph) displayed(id string) string {
	g.t.Helper()
	got := g.ids(`SELECT display_status FROM issue_states WHERE id = ?`, id)
	if len(got) != 1 {
		g.t.Fatalf("issue_states has %d rows for %s, want 1", len(got), id)
	}
	return got[0]
}

func TestAnActiveBlockerBlocksItsDependent(t *testing.T) {
	for _, status := range []string{"ready", "in-progress", "pending-review", "held"} {
		t.Run(status, func(t *testing.T) {
			g := newGraph(t)
			g.issue("blocker", status)
			g.issue("dep", "ready")
			g.blocks("blocker", "dep")

			g.wantBlocked("dep")
			if got := g.displayed("dep"); got != "waiting" {
				t.Fatalf("dependent displays as %q, want waiting", got)
			}
		})
	}
}

func TestABlockedIssueKeepsItsStoredStatus(t *testing.T) {
	g := newGraph(t)
	g.issue("blocker", "ready")
	g.issue("dep", "in-progress")
	g.blocks("blocker", "dep")

	g.wantBlocked("dep")
	if got := g.displayed("dep"); got != "in-progress" {
		t.Fatalf("blocked in-progress issue displays as %q, want in-progress", got)
	}
}

func TestADoneOrOmittedBlockerDoesNotBlock(t *testing.T) {
	for _, status := range []string{"done", "omit"} {
		t.Run(status, func(t *testing.T) {
			g := newGraph(t)
			g.issue("blocker", "ready")
			g.issue("dep", "ready")
			g.blocks("blocker", "dep")
			g.wantBlocked("dep")

			g.setStatus("blocker", status)

			g.wantBlocked()
			if got := g.displayed("dep"); got != "ready" {
				t.Fatalf("dependent displays as %q, want ready", got)
			}
		})
	}
}

func TestARetiredBlockerDoesNotBlock(t *testing.T) {
	g := newGraph(t)
	g.issue("blocker", "ready")
	g.issue("dep", "ready")
	g.blocks("blocker", "dep")
	g.wantBlocked("dep")

	g.retire("blocker")

	g.wantBlocked()
	// The link row persists, so restoring the blocker blocks again (ADR-0010).
	g.exec(`UPDATE issues SET retired_at = NULL WHERE id = 'blocker'`)
	g.wantBlocked("dep")
}

func TestRetiredIssuesAreAbsentFromEveryView(t *testing.T) {
	g := newGraph(t)
	g.issue("blocker", "ready")
	g.issue("gone", "ready")
	g.issue("kept", "ready")
	g.blocks("blocker", "gone")
	g.retire("gone")

	if got := g.ids(`SELECT id FROM live_issues ORDER BY id`); !reflect.DeepEqual(got, []string{"blocker", "kept"}) {
		t.Fatalf("live issues = %v, want [blocker kept]", got)
	}
	g.wantBlocked()
	if got := g.ids(`SELECT id FROM issue_states ORDER BY id`); !reflect.DeepEqual(got, []string{"blocker", "kept"}) {
		t.Fatalf("issue_states = %v, want [blocker kept]", got)
	}
	if got := g.ids(`SELECT to_id FROM readiness_edges`); len(got) != 0 {
		t.Fatalf("readiness edges to a retired issue: %v", got)
	}
}

func TestABlockedAncestorBlocksAGrandchild(t *testing.T) {
	g := newGraph(t)
	g.issue("gate", "ready")
	g.issue("epic", "ready")
	g.issue("child", "ready")
	g.issue("grand", "ready")
	g.parent("epic", "child")
	g.parent("child", "grand")
	g.wantBlocked()

	g.blocks("gate", "epic")
	g.wantBlocked("child", "epic", "grand")
	if got := g.displayed("grand"); got != "waiting" {
		t.Fatalf("grandchild displays as %q, want waiting", got)
	}

	g.setStatus("gate", "done")
	g.wantBlocked()
}

func TestAHeldAncestorBlocksAGrandchild(t *testing.T) {
	g := newGraph(t)
	g.issue("epic", "ready")
	g.issue("child", "in-progress")
	g.issue("grand", "ready")
	g.parent("epic", "child")
	g.parent("child", "grand")

	g.setStatus("epic", "held")

	// The held issue itself isn't blocked; its descendants are, and they keep
	// their stored status (ADR-0009).
	g.wantBlocked("child", "grand")
	if got := g.displayed("epic"); got != "held" {
		t.Fatalf("held ancestor displays as %q, want held", got)
	}
	if got := g.displayed("child"); got != "in-progress" {
		t.Fatalf("child displays as %q, want in-progress", got)
	}
	if got := g.displayed("grand"); got != "waiting" {
		t.Fatalf("grandchild displays as %q, want waiting", got)
	}

	g.setStatus("epic", "ready")
	g.wantBlocked()
	if got := g.displayed("grand"); got != "ready" {
		t.Fatalf("grandchild displays as %q after the hold ends, want ready", got)
	}
}

func TestOpenChildrenMakeAParentWaitButNotBlocked(t *testing.T) {
	g := newGraph(t)
	g.issue("epic", "ready")
	g.issue("child", "ready")
	g.issue("grand", "ready")
	g.issue("sibling", "ready")
	g.parent("epic", "child")
	g.parent("child", "grand")
	g.parent("epic", "sibling")

	// Not a blocking rule: nothing is blocked and nothing propagates (ADR-0016).
	g.wantBlocked()
	for id, want := range map[string]string{"epic": "waiting", "child": "waiting", "grand": "ready", "sibling": "ready"} {
		if got := g.displayed(id); got != want {
			t.Errorf("%s displays as %q, want %q", id, got, want)
		}
	}
	open := `SELECT id FROM open_children WHERE parent_id = ? ORDER BY id`
	if got := g.ids(open, "epic"); !reflect.DeepEqual(got, []string{"child", "grand", "sibling"}) {
		t.Fatalf("epic's open children = %v, want [child grand sibling]", got)
	}

	// Children that are done, omitted, or retired are no longer open.
	g.setStatus("grand", "done")
	g.setStatus("child", "omit")
	g.retire("sibling")
	if got := g.ids(open, "epic"); len(got) != 0 {
		t.Fatalf("epic's open children = %v, want none", got)
	}
	if got := g.displayed("epic"); got != "ready" {
		t.Fatalf("epic displays as %q once its children finish, want ready", got)
	}
}

func TestOpenChildrenCountDescendantsBelowAFinishedChild(t *testing.T) {
	g := newGraph(t)
	g.issue("epic", "ready")
	g.issue("child", "done")
	g.issue("grand", "in-progress")
	g.parent("epic", "child")
	g.parent("child", "grand")

	got := g.ids(`SELECT id FROM open_children WHERE parent_id = 'epic'`)
	if !reflect.DeepEqual(got, []string{"grand"}) {
		t.Fatalf("epic's open children = %v, want [grand]", got)
	}
	if got := g.displayed("epic"); got != "waiting" {
		t.Fatalf("epic displays as %q, want waiting", got)
	}
}

func TestAncestorsRunNearestFirst(t *testing.T) {
	g := newGraph(t)
	for _, id := range []string{"a", "b", "c", "d"} {
		g.issue(id, "ready")
	}
	g.parent("a", "b")
	g.parent("b", "c")
	g.parent("c", "d")

	got := g.ids(`SELECT ancestor_id FROM issue_ancestors WHERE id = 'd' ORDER BY depth`)
	if !reflect.DeepEqual(got, []string{"c", "b", "a"}) {
		t.Fatalf("ancestors of d = %v, want [c b a]", got)
	}
}

func TestOnlyBlocksAndParentLinksAffectReadiness(t *testing.T) {
	g := newGraph(t)
	g.issue("a", "held")
	g.issue("b", "ready")
	g.link("a", "related", "b")
	g.link("b", "discovered-from", "a")

	g.wantBlocked()
	if got := g.displayed("b"); got != "ready" {
		t.Fatalf("b displays as %q, want ready", got)
	}
	if got := g.ids(`SELECT to_id FROM readiness_edges`); len(got) != 0 {
		t.Fatalf("readiness edges = %v, want none", got)
	}
}

// Cycles can't be created through tess, but the views must still terminate
// if a database ever holds one (ADR-0016's internal guard).
func TestTheViewsTerminateOnACycle(t *testing.T) {
	g := newGraph(t)
	g.issue("gate", "ready")
	g.issue("a", "ready")
	g.issue("b", "ready")
	g.issue("c", "ready")
	g.blocks("gate", "a")
	// b and c are each other's parent: a loop below the blocked issue a.
	g.parent("a", "b")
	g.exec(`DROP INDEX links_one_parent`)
	g.parent("b", "c")
	g.parent("c", "b")

	g.wantBlocked("a", "b", "c")
	if got := g.ids(`SELECT DISTINCT id FROM open_children WHERE parent_id = 'a' ORDER BY id`); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Fatalf("a's open children = %v, want [b c]", got)
	}
}

func TestBlockingReachesDownADeepHierarchy(t *testing.T) {
	g := newGraph(t)
	const depth = 60
	g.issue("gate", "ready")
	g.issue("n0", "ready")
	g.blocks("gate", "n0")
	for i := 1; i <= depth; i++ {
		g.issue(fmt.Sprintf("n%d", i), "ready")
		g.parent(fmt.Sprintf("n%d", i-1), fmt.Sprintf("n%d", i))
	}

	if got := g.blocked(); len(got) != depth+1 {
		t.Fatalf("%d issues blocked, want %d: %s", len(got), depth+1, strings.Join(got, " "))
	}
}

func TestTheSchemaRefusesLinksItCannotMean(t *testing.T) {
	g := newGraph(t)
	for _, id := range []string{"a", "b", "c"} {
		g.issue(id, "ready")
	}
	g.parent("a", "c")
	g.link("a", "related", "b")

	ctx := context.Background()
	refused := map[string][3]string{
		"a second parent":             {"b", "parent", "c"},
		"a link to itself":            {"a", "blocks", "a"},
		"an unknown type":             {"a", "duplicates", "b"},
		"the same link twice":         {"a", "parent", "c"},
		"a related link, reversed":    {"b", "related", "a"},
		"a link to a missing issue":   {"a", "blocks", "nope"},
		"a link from a missing issue": {"nope", "blocks", "a"},
	}
	for name, l := range refused {
		err := g.s.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO links (from_id, type, to_id) VALUES (?, ?, ?)`, l[0], l[1], l[2])
			return err
		})
		if err == nil {
			t.Errorf("the schema accepted %s (%v)", name, l)
		}
	}
}
