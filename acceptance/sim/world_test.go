package sim

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	tsync "github.com/plongitudes/tesserae/internal/sync"
	"github.com/plongitudes/tesserae/internal/tracker"
)

// A world is one run: a bare remote and two or three copies, one shared
// clock, and a record of every event and comment any copy made, which is
// what the invariants are checked against.
type world struct {
	t      testing.TB
	ctx    context.Context
	dir    string
	remote string
	copies []*copy
	clock  *clock
	log    []string

	events    map[string]recordedEvent // by event ID
	bases     map[string]unitSet       // an issue's state right after creation
	comments  map[string]bool          // comment IDs any copy made
	resolved  []tsync.Resolution       // every resolution any sync reported
	collision bool                     // a sync was refused with conflict (ADR-0023)
	doomed    map[string]string        // issue ID -> copy whose record of it the merge drops whole (ADR-0023)
	prefix    string
}

type copy struct {
	name, dir, db string
	tr            tracker.Tracker
	close         func() error
	known         []string // IDs this copy has, in creation order
}

type recordedEvent struct {
	issue string
	ev    tracker.Event
	// For a status or status_reason event: the issue's stored status and
	// reason on the copy right after the operation, as JSON text. The two
	// are one unit of the merge, so the latest event of either decides both.
	status, reason string
}

// clock hands out strictly increasing seconds, so no two events share a
// time and every ordering question has one answer.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(time.Second)
	return c.t
}

func newWorld(t testing.TB, copies int) *world {
	t.Helper()
	dir := t.TempDir()
	gitconfig := filepath.Join(dir, "gitconfig")
	if err := os.WriteFile(gitconfig, []byte("[user]\n\tname = sim\n\temail = sim@example.com\n[init]\n\tdefaultBranch = main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)
	t.Setenv("GIT_CONFIG_GLOBAL", gitconfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, v := range []string{"TESS_NOW", "TESS_ACTOR", "CLAUDECODE", "CLAUDE_CODE_SESSION_ID"} {
		t.Setenv(v, "")
	}
	w := &world{
		t:        t,
		ctx:      context.Background(),
		dir:      dir,
		remote:   filepath.Join(dir, "remote.git"),
		clock:    &clock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		events:   map[string]recordedEvent{},
		bases:    map[string]unitSet{},
		comments: map[string]bool{},
		doomed:   map[string]string{},
		prefix:   "t",
	}
	w.git("", "init", "-q", "--bare", w.remote)
	for i := 0; i < copies; i++ {
		name := string(rune('a' + i))
		cdir := filepath.Join(dir, name)
		w.git("", "init", "-q", cdir)
		w.git(cdir, "remote", "add", "origin", w.remote)
		db := filepath.Join(cdir, ".git", "tess.db")
		if err := tracker.Init(w.ctx, db, w.prefix); err != nil {
			t.Fatal(err)
		}
		tr, closeDB, err := tracker.Open(w.ctx, db, w.clock.Now, name)
		if err != nil {
			t.Fatal(err)
		}
		c := &copy{name: name, dir: cdir, db: db, tr: tr, close: closeDB}
		w.copies = append(w.copies, c)
		t.Cleanup(func() { _ = closeDB() })
	}
	return w
}

func (w *world) git(dir string, args ...string) string {
	w.t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		w.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (w *world) logf(format string, args ...any) {
	w.log = append(w.log, fmt.Sprintf(format, args...))
}

// refused reports whether err is a refusal the tracker makes on purpose
// (blocked, cycle, retired, ...), which the simulator treats as a no-op.
func refused(err error) (string, bool) {
	var e *tracker.Error
	if errors.As(err, &e) {
		return e.Code, true
	}
	return "", false
}

// record notes every event on the issues named that the world hasn't seen
// yet, and the issue's base state if it is new. Events a copy makes on a
// record the merge is going to drop whole (a doomed collider) are not
// noted: no copy keeps them.
func (w *world) record(c *copy, ids ...string) {
	w.t.Helper()
	for _, id := range ids {
		if w.doomed[id] == c.name {
			continue
		}
		iss, err := c.tr.Get(w.ctx, id, tracker.WithHistory())
		if err != nil {
			if _, ok := refused(err); ok {
				continue
			}
			w.t.Fatalf("%s: get %s: %v", c.name, id, err)
		}
		if _, ok := w.bases[id]; !ok {
			w.bases[id] = baseUnits(iss)
		}
		status := iss.Status
		if status == "waiting" {
			status = "ready"
		}
		for _, ev := range iss.History {
			if _, seen := w.events[ev.ID]; !seen {
				rec := recordedEvent{issue: id, ev: ev}
				if ev.Attr == "status" || ev.Attr == "status_reason" {
					rec.status, rec.reason = jsonText(status), jsonText(optString(iss.StatusReason))
				}
				w.events[ev.ID] = rec
			}
		}
	}
}

// --- operations -----------------------------------------------------------

func (w *world) create(c *copy, title string, parent string, blockedBy []string) (string, bool) {
	w.t.Helper()
	iss, err := c.tr.Create(w.ctx, tracker.NewIssue{Title: title, Parent: parent, BlockedBy: blockedBy})
	if err != nil {
		if code, ok := refused(err); ok {
			w.logf("%s: create %q parent=%q blocked_by=%v -> %s", c.name, title, parent, blockedBy, code)
			return "", false
		}
		w.t.Fatalf("%s: create: %v", c.name, err)
	}
	w.logf("%s: create %q parent=%q blocked_by=%v -> %s", c.name, title, parent, blockedBy, iss.ID)
	c.known = append(c.known, iss.ID)
	w.record(c, iss.ID)
	return iss.ID, true
}

func (w *world) update(c *copy, id string, p tracker.Patch) {
	w.t.Helper()
	_, err := c.tr.Update(w.ctx, id, p)
	if err != nil {
		if code, ok := refused(err); ok {
			w.logf("%s: update %s %s -> %s", c.name, id, describePatch(p), code)
			return
		}
		w.t.Fatalf("%s: update %s: %v", c.name, id, err)
	}
	w.logf("%s: update %s %s", c.name, id, describePatch(p))
	w.record(c, id)
}

func (w *world) status(c *copy, id, status, reason string, force bool) {
	w.t.Helper()
	_, err := c.tr.Status(w.ctx, []string{id}, status, reason, force)
	if err != nil {
		if code, ok := refused(err); ok {
			w.logf("%s: status %s %s -> %s", c.name, id, status, code)
			return
		}
		w.t.Fatalf("%s: status %s %s: %v", c.name, id, status, err)
	}
	w.logf("%s: status %s %s", c.name, id, status)
	w.record(c, id)
}

func (w *world) link(c *copy, a, wording, b string, unlink bool) {
	w.t.Helper()
	verb := "link"
	f := c.tr.Link
	if unlink {
		verb, f = "unlink", c.tr.Unlink
	}
	_, err := f(w.ctx, []string{a}, wording, []string{b})
	if err != nil {
		if code, ok := refused(err); ok {
			w.logf("%s: %s %s %s %s -> %s", c.name, verb, a, wording, b, code)
			return
		}
		w.t.Fatalf("%s: %s %s %s %s: %v", c.name, verb, a, wording, b, err)
	}
	w.logf("%s: %s %s %s %s", c.name, verb, a, wording, b)
	w.record(c, a, b)
}

func (w *world) comment(c *copy, id, text string) {
	w.t.Helper()
	cm, err := c.tr.Comment(w.ctx, id, text)
	if err != nil {
		if code, ok := refused(err); ok {
			w.logf("%s: comment %s -> %s", c.name, id, code)
			return
		}
		w.t.Fatalf("%s: comment %s: %v", c.name, id, err)
	}
	w.logf("%s: comment %s %q", c.name, id, text)
	w.comments[cm.ID] = true
}

func (w *world) retire(c *copy, id string) {
	w.t.Helper()
	_, err := c.tr.Retire(w.ctx, []string{id}, "")
	if err != nil {
		if code, ok := refused(err); ok {
			w.logf("%s: retire %s -> %s", c.name, id, code)
			return
		}
		w.t.Fatalf("%s: retire %s: %v", c.name, id, err)
	}
	w.logf("%s: retire %s", c.name, id)
	w.record(c, id)
}

func (w *world) restore(c *copy, id string) {
	w.t.Helper()
	_, err := c.tr.Restore(w.ctx, id)
	if err != nil {
		if code, ok := refused(err); ok {
			w.logf("%s: restore %s -> %s", c.name, id, code)
			return
		}
		w.t.Fatalf("%s: restore %s: %v", c.name, id, err)
	}
	w.logf("%s: restore %s", c.name, id)
	w.record(c, id)
}

// sync runs a sync on a copy. A conflict (an ID collision, ADR-0023) ends
// the run's usefulness: it is noted, and the caller stops. Any other error
// fails the test.
func (w *world) sync(c *copy, dryRun bool) (*tsync.Result, bool) {
	w.t.Helper()
	res, err := tsync.Run(w.ctx, tsync.Options{
		Dir: c.dir, DBPath: c.db, Remote: "origin", DryRun: dryRun, Now: w.clock.Now, Actor: c.name,
	})
	if err != nil {
		if code, ok := refused(err); ok && code == "conflict" {
			w.logf("%s: sync -> conflict (%v)", c.name, err)
			w.collision = true
			return nil, false
		}
		w.t.Fatalf("%s: sync: %v\n%s", c.name, err, strings.Join(w.log, "\n"))
	}
	w.logf("%s: sync%s -> fetched %d pushed %d resolved %d", c.name, map[bool]string{true: " --dry-run"}[dryRun], res.Fetched, res.Pushed, len(res.Resolved))
	for _, r := range res.Resolved {
		w.logf("    %s %s: kept %v, dropped %v (%s, %s)", r.Issue, r.Field, r.Kept, r.Dropped, r.Won, r.Reason)
	}
	if !dryRun {
		w.resolved = append(w.resolved, res.Resolved...)
		snap := w.load(c)
		c.known = c.known[:0]
		for id := range snap.Issues {
			c.known = append(c.known, id)
		}
		sort.Strings(c.known)
	}
	return res, true
}

func (w *world) load(c *copy) *tsync.Snapshot {
	w.t.Helper()
	snap, err := tsync.Load(w.ctx, c.db)
	if err != nil {
		w.t.Fatalf("%s: load: %v", c.name, err)
	}
	return snap
}

// settle syncs every copy twice, so every copy has seen every other's
// changes, and returns false if a collision stopped it.
func (w *world) settle() bool {
	w.t.Helper()
	for round := 0; round < 2; round++ {
		for _, c := range w.copies {
			if _, ok := w.sync(c, false); !ok {
				return false
			}
		}
	}
	return true
}

// --- units ------------------------------------------------------------------

// A unitSet is one issue's state as the merge sees it: scalar units by
// name, each as canonical JSON text, and collection units as sets of
// elements. related is kept globally, by pair, in relatedSet.
type unitSet struct {
	scalars map[string]string
	sets    map[string]map[string]bool
}

var scalarAttrs = []string{"title", "type", "priority", "description", "design", "acceptance_criteria", "notes",
	"status", "status_reason", "assignee", "owner", "due_at", "parent", "retired_at"}
var setAttrs = []string{"labels", "blocked_by", "discovered_from"}

func jsonText(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func textHash(body string) string {
	h := sha256.Sum256([]byte(body))
	return hex.EncodeToString(h[:])
}

func optString(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func optTime(p *time.Time) any {
	if p == nil {
		return nil
	}
	return p.UTC().Format(time.RFC3339)
}

// baseUnits is an issue as the tracker shows it right after creation:
// where every unit starts before its events.
func baseUnits(iss tracker.Issue) unitSet {
	status := iss.Status
	if status == "waiting" {
		status = "ready"
	}
	u := unitSet{scalars: map[string]string{}, sets: map[string]map[string]bool{}}
	for k, v := range map[string]any{
		"title": iss.Title, "type": iss.Type, "priority": iss.Priority,
		"description": textHash(iss.Description), "design": textHash(iss.Design),
		"acceptance_criteria": textHash(iss.AcceptanceCriteria), "notes": textHash(iss.Notes),
		"status": status, "status_reason": optString(iss.StatusReason),
		"assignee": optString(iss.Assignee), "owner": optString(iss.Owner),
		"due_at": optTime(iss.DueAt), "parent": optString(iss.Parent), "retired_at": optTime(iss.RetiredAt),
	} {
		u.scalars[k] = jsonText(v)
	}
	for _, k := range setAttrs {
		u.sets[k] = map[string]bool{}
	}
	for _, l := range iss.Labels {
		u.sets["labels"][l] = true
	}
	if iss.Links != nil {
		for _, id := range iss.Links.BlockedBy {
			u.sets["blocked_by"][id] = true
		}
		for _, id := range iss.Links.DiscoveredFrom {
			u.sets["discovered_from"][id] = true
		}
	}
	return u
}

// snapshotUnits is an issue as a snapshot holds it.
func snapshotUnits(iss *tsync.Issue) unitSet {
	u := unitSet{scalars: map[string]string{}, sets: map[string]map[string]bool{}}
	for k, v := range map[string]any{
		"title": iss.Title, "type": iss.Type, "priority": iss.Priority,
		"description": iss.Description, "design": iss.Design,
		"acceptance_criteria": iss.AcceptanceCriteria, "notes": iss.Notes,
		"status": iss.Status, "status_reason": optString(iss.StatusReason),
		"assignee": optString(iss.Assignee), "owner": optString(iss.Owner),
		"due_at": optTime(iss.DueAt), "parent": optString(iss.Parent), "retired_at": optTime(iss.RetiredAt),
	} {
		u.scalars[k] = jsonText(v)
	}
	for _, k := range setAttrs {
		u.sets[k] = map[string]bool{}
	}
	for _, l := range iss.Labels {
		u.sets["labels"][l] = true
	}
	for _, id := range iss.BlockedBy {
		u.sets["blocked_by"][id] = true
	}
	for _, id := range iss.DiscoveredFrom {
		u.sets["discovered_from"][id] = true
	}
	return u
}

func pairKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

func describePatch(p tracker.Patch) string {
	var parts []string
	if p.Title != nil {
		parts = append(parts, fmt.Sprintf("title=%q", *p.Title))
	}
	if p.Priority != nil {
		parts = append(parts, fmt.Sprintf("priority=%d", *p.Priority))
	}
	if p.Description != nil {
		parts = append(parts, fmt.Sprintf("description=%q", *p.Description))
	}
	if p.Assignee != nil {
		parts = append(parts, fmt.Sprintf("assignee=%q", *p.Assignee))
	}
	if p.Notes != nil {
		parts = append(parts, fmt.Sprintf("notes=%q append=%v", *p.Notes, p.Append))
	}
	if len(p.AddLabels) > 0 {
		parts = append(parts, fmt.Sprintf("add-label=%v", p.AddLabels))
	}
	if len(p.RemoveLabels) > 0 {
		parts = append(parts, fmt.Sprintf("remove-label=%v", p.RemoveLabels))
	}
	return strings.Join(parts, " ")
}
