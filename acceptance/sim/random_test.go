package sim

import (
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"testing"

	"github.com/plongitudes/tesserae/internal/tracker"
)

// TestRandomRunsConverge is the property test: random operations on two or
// three copies, syncs at random moments, then every invariant.
func TestRandomRunsConverge(t *testing.T) {
	runs := 25
	if testing.Short() {
		runs = 5
	}
	if s := os.Getenv("TESS_SIM_RUNS"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			t.Fatalf("TESS_SIM_RUNS=%q: %v", s, err)
		}
		runs = n
	}
	seeds := make([]int64, 0, runs)
	if s := os.Getenv("TESS_SIM_SEED"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			t.Fatalf("TESS_SIM_SEED=%q: %v", s, err)
		}
		seeds = append(seeds, n)
	} else {
		for i := 1; i <= runs; i++ {
			seeds = append(seeds, int64(i))
		}
	}
	collisions := 0
	for _, seed := range seeds {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			if !randomRun(t, seed) {
				collisions++
			}
		})
	}
	if collisions > 0 {
		t.Logf("%d of %d runs ended in an ID collision (ADR-0023) and were not checked", collisions, len(seeds))
	}
}

// randomRun plays one seed and returns false if an ID collision ended it.
func randomRun(t *testing.T, seed int64) bool {
	rng := rand.New(rand.NewSource(seed))
	w := newWorld(t, 2+rng.Intn(2))
	ops := 20 + rng.Intn(30)
	for i := 0; i < ops; i++ {
		c := w.copies[rng.Intn(len(w.copies))]
		if !w.randomOp(rng, c, i) {
			return false
		}
	}
	if !w.settle() {
		return false
	}
	w.check()
	return true
}

func (w *world) pick(rng *rand.Rand, c *copy) (string, bool) {
	if len(c.known) == 0 {
		return "", false
	}
	return c.known[rng.Intn(len(c.known))], true
}

var statuses = []string{"ready", "in-progress", "pending-review", "held", "done", "omit"}
var wordings = []string{"blocks", "blocked-by", "parent-of", "child-of", "related", "discovered-from"}

// randomOp applies one operation and returns false on an ID collision.
func (w *world) randomOp(rng *rand.Rand, c *copy, n int) bool {
	switch roll := rng.Intn(100); {
	case roll < 18 || len(c.known) == 0:
		title := fmt.Sprintf("Issue %d from %s", n, c.name)
		var parent string
		var blockedBy []string
		if id, ok := w.pick(rng, c); ok && rng.Intn(4) == 0 {
			parent = id
		}
		if id, ok := w.pick(rng, c); ok && rng.Intn(4) == 0 {
			blockedBy = []string{id}
		}
		w.create(c, title, parent, blockedBy)
		return true
	case roll < 40:
		id, _ := w.pick(rng, c)
		var p tracker.Patch
		switch rng.Intn(6) {
		case 0:
			s := fmt.Sprintf("Title %d by %s", n, c.name)
			p.Title = &s
		case 1:
			pr := rng.Intn(5)
			p.Priority = &pr
		case 2:
			s := fmt.Sprintf("Description %d by %s", n, c.name)
			p.Description = &s
		case 3:
			s := []string{"", c.name, "someone"}[rng.Intn(3)]
			p.Assignee = &s
		case 4:
			p.AddLabels = []string{[]string{"ui", "api", "docs"}[rng.Intn(3)]}
		case 5:
			p.RemoveLabels = []string{[]string{"ui", "api", "docs"}[rng.Intn(3)]}
		}
		w.update(c, id, p)
		return true
	case roll < 55:
		id, _ := w.pick(rng, c)
		status := statuses[rng.Intn(len(statuses))]
		reason := ""
		if status == "held" || status == "omit" {
			reason = "because"
		}
		w.status(c, id, status, reason, rng.Intn(5) == 0)
		return true
	case roll < 70:
		a, _ := w.pick(rng, c)
		b, _ := w.pick(rng, c)
		w.link(c, a, wordings[rng.Intn(len(wordings))], b, rng.Intn(4) == 0)
		return true
	case roll < 76:
		id, _ := w.pick(rng, c)
		w.comment(c, id, fmt.Sprintf("Comment %d by %s", n, c.name))
		return true
	case roll < 82:
		id, _ := w.pick(rng, c)
		if rng.Intn(3) == 0 {
			w.restore(c, id)
		} else {
			w.retire(c, id)
		}
		return true
	default:
		_, ok := w.sync(c, rng.Intn(6) == 0)
		return ok
	}
}
