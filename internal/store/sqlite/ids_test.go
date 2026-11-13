package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/plongitudes/tesserae/internal/ids"
)

func (g *graph) candidates(input string) []IDCandidate {
	g.t.Helper()
	ctx := context.Background()
	var got []IDCandidate
	err := g.s.Read(ctx, func(tx *sql.Tx) (err error) {
		got, err = IDCandidates(ctx, tx, input)
		return err
	})
	if err != nil {
		g.t.Fatalf("IDCandidates(%q): %v", input, err)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].ID < got[j].ID })
	return got
}

func (g *graph) wantCandidates(input string, want ...IDCandidate) {
	g.t.Helper()
	if got := g.candidates(input); !reflect.DeepEqual(got, want) {
		g.t.Errorf("IDCandidates(%q) = %v; want %v", input, got, want)
	}
}

func TestIDCandidatesAreTheIssuesWhoseIDContainsTheInput(t *testing.T) {
	g := newGraph(t)
	g.issue("t-abc", "ready")
	g.issue("t-abd", "done")
	g.issue("t-xyz", "ready")

	g.wantCandidates("ab", IDCandidate{ID: "t-abc"}, IDCandidate{ID: "t-abd"})
	g.wantCandidates("t-x", IDCandidate{ID: "t-xyz"})
	g.wantCandidates("zzz")
}

func TestIDCandidatesIncludeRetiredIssuesAndSaySo(t *testing.T) {
	g := newGraph(t)
	g.issue("t-abc", "ready")
	g.issue("t-abd", "ready")
	g.retire("t-abc")

	g.wantCandidates("ab", IDCandidate{ID: "t-abc", Retired: true}, IDCandidate{ID: "t-abd"})
	g.wantCandidates("t-abc", IDCandidate{ID: "t-abc", Retired: true})
}

func TestIDCandidatesForAnExactIDAreThatIssueAlone(t *testing.T) {
	g := newGraph(t)
	g.issue("bd-abc", "ready")
	g.issue("bd-abc.1", "ready")
	g.issue("bd-abc.10", "ready")

	g.wantCandidates("bd-abc.1", IDCandidate{ID: "bd-abc.1"})
	g.wantCandidates("bd-abc", IDCandidate{ID: "bd-abc"})
}

func TestIDCandidatesTakeTheInputLiterally(t *testing.T) {
	g := newGraph(t)
	g.issue("t-abc", "ready")
	g.issue("t_abc", "ready")

	// Neither a LIKE wildcard nor a different case matches.
	g.wantCandidates("%")
	g.wantCandidates("_a", IDCandidate{ID: "t_abc"})
	g.wantCandidates("ABC")
	g.wantCandidates("")
}

// The store's candidates and the ids rules together, as the tracker will
// use them.
func TestPartialIDsResolveOverTheStore(t *testing.T) {
	g := newGraph(t)
	g.issue("t-abc", "ready")
	g.issue("t-abd", "ready")
	g.issue("t-xyz", "ready")
	g.issue("bd-q1.1", "ready")
	g.issue("t-old", "ready")
	g.retire("t-old")

	resolve := func(input string) (string, error) {
		var cs []ids.Candidate
		for _, c := range g.candidates(input) {
			cs = append(cs, ids.Candidate(c))
		}
		return ids.Resolve(input, cs)
	}

	for input, want := range map[string]string{
		"t-abc": "t-abc", "t-x": "t-xyz", "yz": "t-xyz", "bd-q1.1": "bd-q1.1", "q1": "bd-q1.1", "t-old": "t-old",
	} {
		if got, err := resolve(input); err != nil || got != want {
			t.Errorf("resolve(%q) = %q, %v; want %q", input, got, err, want)
		}
	}

	var amb *ids.AmbiguousError
	if _, err := resolve("t-ab"); !errors.As(err, &amb) || !reflect.DeepEqual(amb.Candidates, []string{"t-abc", "t-abd"}) {
		t.Errorf("resolve(t-ab) error = %v; want ambiguous between t-abc and t-abd", err)
	}
	var nf *ids.NotFoundError
	for _, input := range []string{"old", "t-ol", "nope"} {
		if _, err := resolve(input); !errors.As(err, &nf) {
			t.Errorf("resolve(%q) error = %v; want not found", input, err)
		}
	}
}

func TestIssueCountCountsRetiredIssuesToo(t *testing.T) {
	g := newGraph(t)
	ctx := context.Background()
	count := func() (n int) {
		t.Helper()
		err := g.s.Read(ctx, func(tx *sql.Tx) (err error) {
			n, err = IssueCount(ctx, tx)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	if n := count(); n != 0 {
		t.Fatalf("IssueCount on an empty store = %d", n)
	}
	g.issue("t-abc", "ready")
	g.issue("t-abd", "ready")
	g.retire("t-abd")
	if n := count(); n != 2 {
		t.Fatalf("IssueCount = %d; want 2", n)
	}
}

// An ID is allocated and its issue inserted in one write transaction, so a
// collision is seen and stepped around before the insert.
func TestAllocatingAnIDInTheCreateTransactionAvoidsATakenID(t *testing.T) {
	g := newGraph(t)
	ctx := context.Background()
	seed := ids.Seed{Prefix: "t", Title: "Same title", Creator: "tester", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}

	create := func() (id string) {
		t.Helper()
		err := g.s.Write(ctx, func(tx *sql.Tx) error {
			n, err := IssueCount(ctx, tx)
			if err != nil {
				return err
			}
			id, err = ids.Allocate(seed, n, func(id string) (bool, error) { return IssueExists(ctx, tx, id) })
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx,
				`INSERT INTO issues (id, title, created_at, created_by, updated_at)
				 VALUES (?, ?, '2026-01-01T00:00:00Z', 'tester', '2026-01-01T00:00:00Z')`, id, seed.Title)
			return err
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		return id
	}

	// The same seed three times over: every create after the first collides
	// on nonce 0, and the second issue is retired to show that a retired
	// issue's ID stays taken.
	first := create()
	if want := ids.Generate(seed, 0, ids.MinLength); first != want {
		t.Fatalf("first ID = %q; want %q", first, want)
	}
	second := create()
	g.retire(second)
	third := create()
	if first == second || first == third || second == third {
		t.Fatalf("IDs collide: %q, %q, %q", first, second, third)
	}
}
