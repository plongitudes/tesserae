package sim

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/plongitudes/tesserae/internal/ids"
	tsync "github.com/plongitudes/tesserae/internal/sync"
	"github.com/plongitudes/tesserae/internal/tracker"
)

// Red-team scenarios: each is a script of operations on named copies,
// written to break the merge, with expectations checked where they stand
// and every invariant checked after the copies settle. Add a scenario for
// every merge bug found; none is ever removed.
//
// Lines:
//
//	COPY: create NAME [parent NAME] [blocked-by NAME]
//	COPY: update NAME title|priority|description|assignee|add-label|remove-label VALUE...
//	COPY: status NAME STATUS [REASON...] [force]
//	COPY: link NAME WORDING NAME        COPY: unlink NAME WORDING NAME
//	COPY: comment NAME TEXT...          COPY: retire NAME          COPY: restore NAME
//	COPY: sync [dry-run]                COPY: lose-lkg (delete the local sync ref)
//	COPY: collide NAME COPY NAME        two issues, one on each copy, with one ID (ADR-0023)
//	expect COPY NAME ATTR VALUE         a scalar unit, VALUE as JSON or a bare string
//	expect COPY NAME has|lacks ATTR ELEMENT
//	expect COPY NAME missing
//	expect COPY sync CODE               the next sync on COPY fails with CODE
//	expect resolved NAME FIELD REASON   some sync so far reported this
//	no-settle                           skip the final settle and checks
var scenarios = map[string]string{
	"change and change back wins by its later event": `
		a: create X
		a: sync
		b: sync
		b: update X title Edited on b
		a: update X title Edited on a
		a: update X title X
		expect a X title "X"
	`,
	"a label removed then re-added survives the other copy's removal": `
		a: create X
		a: update X add-label ui
		a: sync
		b: sync
		a: update X remove-label ui
		b: update X remove-label ui
		b: update X add-label ui
		expect a X has labels ui
	`,
	"three copies, the latest edit anywhere wins": `
		a: create X
		a: sync
		b: sync
		c: sync
		b: update X title Second
		a: update X title Third
		c: update X title Fourth
		b: sync
		a: sync
		c: sync
		expect a X title "Fourth"
	`,
	"an edit after a retire on the other copy is dropped": `
		a: create X
		a: sync
		b: sync
		a: retire X
		b: update X title Too late
		expect a X title "X"
		expect resolved X title retired
	`,
	"an edit before a retire on the other copy stands": `
		a: create X
		a: sync
		b: sync
		b: update X title In time
		a: retire X
		expect a X title "In time"
	`,
	"a later restore lets the other copy's edit stand": `
		a: create X
		a: sync
		b: sync
		a: retire X
		b: update X title Edited while retired elsewhere
		a: restore X
		expect a X retired_at null
		expect a X title "Edited while retired elsewhere"
	`,
	"retire on one copy, restore on the other, later wins": `
		a: create X
		a: retire X
		a: sync
		b: sync
		a: restore X
		b: restore X
		b: retire X
		expect a X retired_at null
		a: sync
		b: sync
		expect b X retired_at null
	`,
	"blocks links that close a loop across copies": `
		a: create X
		a: create Y
		a: sync
		b: sync
		a: link X blocks Y
		b: link Y blocks X
		expect resolved X blocked_by cycle
	`,
	"a parent link made after the other copy's blocks link is the one dropped": `
		a: create P
		a: create C
		a: sync
		b: sync
		b: link C blocks P
		a: link P parent-of C
		expect resolved C parent cycle
		expect a C parent null
		expect a P has blocked_by C
	`,
	"two parents for one child resolve to the later one": `
		a: create P1
		a: create P2
		a: create C
		a: sync
		b: sync
		a: link P1 parent-of C
		b: link P2 parent-of C
		expect a C parent "P2"
	`,
	"status and reason travel together": `
		a: create X
		a: sync
		b: sync
		a: status X held waiting on something
		b: status X done
		expect a X status "done"
		expect a X status_reason null
	`,
	"an unlink propagates and is not resurrected": `
		a: create X
		a: create Y
		a: link X blocks Y
		a: sync
		b: sync
		a: unlink X blocks Y
		b: update Y title Touched elsewhere
		expect b Y lacks blocked_by X
	`,
	"an unlink then a relink on the other copy wins": `
		a: create X
		a: create Y
		a: link X blocks Y
		a: sync
		b: sync
		a: unlink X blocks Y
		b: unlink X blocks Y
		b: link X blocks Y
		expect a Y has blocked_by X
	`,
	"the same title on two copies is two issues": `
		a: create Same
		b: create Same2
		a: sync
		b: sync
		a: sync
		expect a Same title "Same"
		expect a Same2 title "Same2"
	`,
	"related in opposite wordings is one link": `
		a: create X
		a: create Y
		a: sync
		b: sync
		a: link X related Y
		b: link Y related X
	`,
	"a copy that lost its LKG merges without conflict": `
		a: create X
		a: sync
		b: sync
		b: update X title From b
		b: sync
		a: lose-lkg
		a: update X priority 0
		a: sync
		expect a X title "From b"
		expect a X priority 0
	`,
	"comments from both copies are both kept": `
		a: create X
		a: sync
		b: sync
		a: comment X From a
		b: comment X From b
	`,
	"a dry run writes nothing": `
		a: create X
		a: sync dry-run
		b: sync
		expect b X missing
		a: sync
		b: sync
		expect b X title "X"
	`,
	"an ID collision refuses the sync until the collider is retired": `
		a: collide X b Y
		a: sync
		expect b sync conflict
		b: create Z
		b: retire Y
		b: sync
		expect b X title "X"
		expect b X retired_at null
		a: sync
		expect a Z title "Z"
	`,
	"done on one copy, a new blocker on the other": `
		a: create X
		a: create B
		a: sync
		b: sync
		a: status X done
		b: link B blocks X
		expect a X status "done"
		expect a X has blocked_by B
	`,
	"omit with open children made elsewhere": `
		a: create P
		a: sync
		b: sync
		a: status P omit no longer needed
		b: create C parent P
		expect a P status "omit"
		expect a C parent "P"
	`,
}

func TestScenarios(t *testing.T) {
	for name, script := range scenarios {
		t.Run(name, func(t *testing.T) {
			runScenario(t, script)
		})
	}
}

type scenario struct {
	w       *world
	names   map[string]string // NAME -> issue ID
	byID    map[string]string // issue ID -> NAME, for messages
	expects map[string]string // COPY -> code the next sync must fail with
	settle  bool
}

func runScenario(t *testing.T, script string) {
	copies := 2
	if strings.Contains(script, "c:") {
		copies = 3
	}
	s := &scenario{w: newWorld(t, copies), names: map[string]string{}, byID: map[string]string{}, expects: map[string]string{}, settle: true}
	for _, line := range strings.Split(script, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		s.step(line)
	}
	if s.settle {
		if !s.w.settle() {
			s.w.fail("an ID collision ended the scenario")
		}
		s.w.check()
	}
}

func (s *scenario) copy(name string) *copy {
	for _, c := range s.w.copies {
		if c.name == name {
			return c
		}
	}
	s.w.fail("no copy %q", name)
	return nil
}

func (s *scenario) id(name string) string {
	id, ok := s.names[name]
	if !ok {
		s.w.fail("no issue named %q yet", name)
	}
	return id
}

func (s *scenario) step(line string) {
	w := s.w
	if line == "no-settle" {
		s.settle = false
		return
	}
	if strings.HasPrefix(line, "expect ") {
		s.expect(strings.Fields(line)[1:])
		return
	}
	who, rest, ok := strings.Cut(line, ": ")
	if !ok {
		w.fail("bad scenario line %q", line)
	}
	c := s.copy(who)
	args := strings.Fields(rest)
	switch args[0] {
	case "create":
		name := args[1]
		var parent string
		var blockedBy []string
		for i := 2; i+1 < len(args); i += 2 {
			switch args[i] {
			case "parent":
				parent = s.id(args[i+1])
			case "blocked-by":
				blockedBy = append(blockedBy, s.id(args[i+1]))
			}
		}
		id, ok := w.create(c, name, parent, blockedBy)
		if !ok {
			w.fail("create %s was refused", name)
		}
		s.names[name], s.byID[id] = id, name
	case "update":
		id := s.id(args[1])
		value := strings.Join(args[3:], " ")
		var p tracker.Patch
		switch args[2] {
		case "title":
			p.Title = &value
		case "priority":
			n, err := strconv.Atoi(value)
			if err != nil {
				w.fail("priority %q", value)
			}
			p.Priority = &n
		case "description":
			p.Description = &value
		case "assignee":
			p.Assignee = &value
		case "add-label":
			p.AddLabels = []string{value}
		case "remove-label":
			p.RemoveLabels = []string{value}
		default:
			w.fail("update %s: unknown field %q", args[1], args[2])
		}
		w.update(c, id, p)
	case "status":
		id := s.id(args[1])
		force := false
		reasonWords := args[3:]
		if n := len(reasonWords); n > 0 && reasonWords[n-1] == "force" {
			force, reasonWords = true, reasonWords[:n-1]
		}
		w.status(c, id, args[2], strings.Join(reasonWords, " "), force)
	case "link", "unlink":
		w.link(c, s.id(args[1]), args[2], s.id(args[3]), args[0] == "unlink")
	case "comment":
		w.comment(c, s.id(args[1]), strings.Join(args[2:], " "))
	case "retire":
		w.retire(c, s.id(args[1]))
	case "restore":
		w.restore(c, s.id(args[1]))
	case "sync":
		dry := len(args) > 1 && args[1] == "dry-run"
		if code, ok := s.expects[c.name]; ok {
			delete(s.expects, c.name)
			_, err := tsync.Run(w.ctx, tsync.Options{Dir: c.dir, DBPath: c.db, Remote: "origin", DryRun: dry, Now: w.clock.Now, Actor: c.name})
			got, _ := refused(err)
			w.logf("%s: sync -> %v (expected %s)", c.name, err, code)
			if got != code {
				w.fail("sync on %s: expected code %s, got %v", c.name, code, err)
			}
			return
		}
		if _, ok := w.sync(c, dry); !ok {
			w.fail("sync on %s was refused with conflict", c.name)
		}
	case "lose-lkg":
		w.git(c.dir, "update-ref", "-d", "refs/tess/sync")
		w.logf("%s: lose-lkg", c.name)
	case "collide":
		s.collide(c, args[1], s.copy(args[2]), args[3])
	default:
		w.fail("unknown operation %q", rest)
	}
}

// collide creates NAME on c and NAME2 on c2 so that both get the same ID:
// the second title is searched for, given the clock's next value. It
// relies on Create reading the clock once, which the check after the
// first create confirms.
func (s *scenario) collide(c *copy, name string, c2 *copy, name2 string) {
	w := s.w
	predicted := w.clock.t.Add(1e9)
	id, ok := w.create(c, name, "", nil)
	if !ok {
		w.fail("create %s was refused", name)
	}
	if want := ids.Generate(ids.Seed{Prefix: w.prefix, Title: name, Creator: c.name, CreatedAt: predicted}, 0, 3); want != id {
		w.fail("collide: predicted ID %s for %s, got %s; Create reads the clock other than once", want, name, id)
	}
	s.names[name], s.byID[id] = id, name
	predicted = w.clock.t.Add(1e9)
	title := ""
	for i := 0; ; i++ {
		candidate := fmt.Sprintf("%s %d", name2, i)
		if ids.Generate(ids.Seed{Prefix: w.prefix, Title: candidate, Creator: c2.name, CreatedAt: predicted}, 0, 3) == id {
			title = candidate
			break
		}
	}
	id2, ok := w.create(c2, title, "", nil)
	if !ok {
		w.fail("create %s was refused", title)
	}
	if id2 != id {
		w.fail("collide: %s got %s, not %s", title, id2, id)
	}
	s.names[name2] = id2
}

func (s *scenario) expect(args []string) {
	w := s.w
	switch {
	case args[0] == "resolved":
		id := s.id(args[1])
		for _, r := range w.resolved {
			if r.Issue == id && r.Field == args[2] && r.Reason == args[3] {
				return
			}
		}
		w.fail("no resolution of %s %s with reason %s so far", args[1], args[2], args[3])
	case args[1] == "sync":
		s.expects[args[0]] = args[2]
	default:
		c := s.copy(args[0])
		snap := w.load(c)
		if args[2] == "missing" {
			if id, ok := s.names[args[1]]; ok {
				if _, there := snap.Issues[id]; there {
					w.fail("copy %s has %s, expected it missing", c.name, args[1])
				}
			}
			return
		}
		id := s.id(args[1])
		iss, ok := snap.Issues[id]
		if !ok {
			w.fail("copy %s lacks %s", c.name, args[1])
		}
		u := snapshotUnits(iss)
		switch args[2] {
		case "has", "lacks":
			elem := args[4]
			if other, ok := s.names[elem]; ok {
				elem = other
			}
			if present := u.sets[args[3]][elem]; present != (args[2] == "has") {
				w.fail("copy %s %s %s: has %s = %v", c.name, args[1], args[3], args[4], present)
			}
		default:
			want := strings.Join(args[3:], " ")
			if other, ok := s.names[strings.Trim(want, `"`)]; ok {
				want = jsonText(other)
			}
			if got := u.scalars[args[2]]; got != want {
				w.fail("copy %s %s %s = %s, expected %s", c.name, args[1], args[2], got, want)
			}
		}
	}
}
