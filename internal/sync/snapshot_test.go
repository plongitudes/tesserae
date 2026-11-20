package sync

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/plongitudes/tesserae/internal/tracker"
)

const emptyHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func hashOf(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

func ptr[T any](v T) *T { return &v }

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return t
}

// sample is a snapshot that uses every part of the format: both kinds of
// optional value, every collection out of order, a text with no trailing
// newline, and event values of each JSON kind.
func sample() *Snapshot {
	desc := "Line one\nline two, no newline at the end"
	return &Snapshot{
		Config: Config{Format: 1, Prefix: "t"},
		Issues: map[string]*Issue{
			"t-a3f": {
				ID: "t-a3f", Title: `Fix <the> "parser" & co`,
				Description: hashOf(desc), Design: emptyHash, AcceptanceCriteria: emptyHash, Notes: emptyHash,
				Type: "bug", Priority: 1, Status: "held",
				StatusReason: ptr("waiting on review"), Assignee: ptr("alice"), Owner: nil,
				Labels: []string{"zeta", "alpha"}, Parent: ptr("t-9f2"),
				BlockedBy: []string{"t-zzz", "t-b00"}, Related: []string{"t-b00"}, DiscoveredFrom: []string{"t-9f2"},
				DueAt:     ptr(at("2026-03-01T00:00:00Z")),
				CreatedAt: at("2026-01-02T03:04:05Z"), CreatedBy: "tester",
				UpdatedAt: at("2026-01-03T00:00:00.750+02:00"), RetiredAt: nil,
			},
			"t-9f2": {
				ID: "t-9f2", Title: "Plain",
				Description: emptyHash, Design: emptyHash, AcceptanceCriteria: emptyHash, Notes: emptyHash,
				Type: "task", Priority: 2, Status: "ready",
				CreatedAt: at("2026-01-01T00:00:00Z"), CreatedBy: "tester",
				UpdatedAt: at("2026-01-01T00:00:00Z"), RetiredAt: ptr(at("2026-01-09T00:00:00Z")),
			},
		},
		Comments: map[string]*Comment{
			"01JGC0000000000000000000C1": {
				ID: "01JGC0000000000000000000C1", IssueID: "t-a3f", Author: "bob",
				Text: "looks right\n", CreatedAt: at("2026-01-02T04:00:00Z"),
			},
		},
		Events: map[string]*Event{
			"01JGC0000000000000000000E1": {
				ID: "01JGC0000000000000000000E1", IssueID: "t-a3f", ChangedAt: at("2026-01-02T05:00:00Z"),
				ChangedBy: "alice", Attr: "priority", OldValue: float64(2), NewValue: float64(1),
			},
			"01JGC0000000000000000000E2": {
				ID: "01JGC0000000000000000000E2", IssueID: "t-a3f", ChangedAt: at("2026-01-02T06:00:00Z"),
				ChangedBy: "alice", Attr: "assignee", OldValue: nil, NewValue: "alice",
			},
		},
		Texts: map[string]string{emptyHash: "", hashOf(desc): desc},
	}
}

func TestFilesIsTheCanonicalEncoding(t *testing.T) {
	desc := "Line one\nline two, no newline at the end"
	want := map[string]string{
		"config.json": `{"format":1,"prefix":"t"}` + "\n",
		"issues/t-a3f.json": `{"id":"t-a3f","title":"Fix <the> \"parser\" & co","description":"` + hashOf(desc) +
			`","design":"` + emptyHash + `","acceptance_criteria":"` + emptyHash + `","notes":"` + emptyHash +
			`","type":"bug","priority":1,"status":"held","status_reason":"waiting on review","assignee":"alice","owner":null,` +
			`"labels":["alpha","zeta"],"parent":"t-9f2","blocked_by":["t-b00","t-zzz"],"related":["t-b00"],"discovered_from":["t-9f2"],` +
			`"due_at":"2026-03-01T00:00:00Z","created_at":"2026-01-02T03:04:05Z","created_by":"tester",` +
			`"updated_at":"2026-01-02T22:00:00Z","retired_at":null}` + "\n",
		"issues/t-9f2.json": `{"id":"t-9f2","title":"Plain","description":"` + emptyHash +
			`","design":"` + emptyHash + `","acceptance_criteria":"` + emptyHash + `","notes":"` + emptyHash +
			`","type":"task","priority":2,"status":"ready","status_reason":null,"assignee":null,"owner":null,` +
			`"labels":[],"parent":null,"blocked_by":[],"related":[],"discovered_from":[],` +
			`"due_at":null,"created_at":"2026-01-01T00:00:00Z","created_by":"tester",` +
			`"updated_at":"2026-01-01T00:00:00Z","retired_at":"2026-01-09T00:00:00Z"}` + "\n",
		"comments/t-a3f/01JGC0000000000000000000C1.json": `{"id":"01JGC0000000000000000000C1","issue_id":"t-a3f","author":"bob","text":"looks right\n","created_at":"2026-01-02T04:00:00Z"}` + "\n",
		"events/t-a3f/01JGC0000000000000000000E1.json":   `{"id":"01JGC0000000000000000000E1","issue_id":"t-a3f","changed_at":"2026-01-02T05:00:00Z","changed_by":"alice","attr":"priority","old_value":2,"new_value":1}` + "\n",
		"events/t-a3f/01JGC0000000000000000000E2.json":   `{"id":"01JGC0000000000000000000E2","issue_id":"t-a3f","changed_at":"2026-01-02T06:00:00Z","changed_by":"alice","attr":"assignee","old_value":null,"new_value":"alice"}` + "\n",
		"texts/" + emptyHash:                             "",
		"texts/" + hashOf(desc):                          desc,
	}
	got := sample().Files()
	for path, w := range want {
		g, ok := got[path]
		if !ok {
			t.Errorf("no file %s", path)
			continue
		}
		if string(g) != w {
			t.Errorf("%s:\n got %s\nwant %s", path, g, w)
		}
	}
	for path := range got {
		if _, ok := want[path]; !ok {
			t.Errorf("unexpected file %s", path)
		}
	}
}

func TestFilesLeavesTheSnapshotAsItWas(t *testing.T) {
	s := sample()
	s.Files()
	if got := s.Issues["t-a3f"].Labels; got[0] != "zeta" || got[1] != "alpha" {
		t.Errorf("Files reordered the snapshot's labels: %v", got)
	}
}

func TestFilesRoundTripsThroughFromFiles(t *testing.T) {
	first := sample().Files()
	parsed, err := FromFiles(first)
	if err != nil {
		t.Fatal(err)
	}
	second := parsed.Files()
	wantSameFiles(t, first, second)

	// What came back is the snapshot, not just something that prints alike.
	iss := parsed.Issues["t-a3f"]
	if iss == nil || iss.Title != `Fix <the> "parser" & co` || iss.Priority != 1 || *iss.Parent != "t-9f2" ||
		!iss.DueAt.Equal(at("2026-03-01T00:00:00Z")) || iss.Owner != nil || len(iss.BlockedBy) != 2 {
		t.Errorf("issue parsed back as %+v", iss)
	}
	if ev := parsed.Events["01JGC0000000000000000000E1"]; ev == nil || ev.NewValue != float64(1) || ev.IssueID != "t-a3f" {
		t.Errorf("event parsed back as %+v", ev)
	}
	if c := parsed.Comments["01JGC0000000000000000000C1"]; c == nil || c.Text != "looks right\n" {
		t.Errorf("comment parsed back as %+v", c)
	}
	if len(parsed.Texts) != 2 || parsed.Config != (Config{Format: 1, Prefix: "t"}) {
		t.Errorf("texts %d, config %+v", len(parsed.Texts), parsed.Config)
	}
}

func TestFromFilesRefusesWhatItCannotRead(t *testing.T) {
	cases := map[string]func(files map[string][]byte){
		"no config.json":         func(f map[string][]byte) { delete(f, "config.json") },
		"a file it doesn't know": func(f map[string][]byte) { f["README.md"] = []byte("hi") },
		"an issue under another ID": func(f map[string][]byte) {
			f["issues/t-other.json"] = f["issues/t-9f2.json"]
		},
		"a field it doesn't know": func(f map[string][]byte) {
			f["issues/t-9f2.json"] = bytes.Replace(f["issues/t-9f2.json"], []byte(`{"id"`), []byte(`{"colour":"red","id"`), 1)
		},
		"a text under the wrong hash": func(f map[string][]byte) { f["texts/"+hashOf("x")] = []byte("y") },
		"broken JSON":                 func(f map[string][]byte) { f["issues/t-9f2.json"] = []byte("{") },
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			files := sample().Files()
			breakIt(files)
			if _, err := FromFiles(files); err == nil {
				t.Error("FromFiles accepted it")
			}
		})
	}
}

func TestFromFilesRefusesAFormatItDoesNotKnow(t *testing.T) {
	files := sample().Files()
	files["config.json"] = []byte(`{"format":2,"prefix":"t","more":true}` + "\n")
	files["issues/t-9f2.json"] = []byte(`{"shape":"new"}` + "\n")
	_, err := FromFiles(files)
	var e *tracker.Error
	if !errors.As(err, &e) || e.Code != CodeFormatUnsupported {
		t.Fatalf("got %v, want %s", err, CodeFormatUnsupported)
	}
	if !strings.Contains(e.Hint, "upgrade") {
		t.Errorf("hint %q doesn't say to upgrade", e.Hint)
	}
}

func wantSameFiles(t *testing.T, a, b map[string][]byte) {
	t.Helper()
	paths := map[string]bool{}
	for p := range a {
		paths[p] = true
	}
	for p := range b {
		paths[p] = true
	}
	sorted := make([]string, 0, len(paths))
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)
	for _, p := range sorted {
		x, inA := a[p]
		y, inB := b[p]
		switch {
		case !inA:
			t.Errorf("%s: only in the second", p)
		case !inB:
			t.Errorf("%s: only in the first", p)
		case !bytes.Equal(x, y):
			t.Errorf("%s differs:\n%s---\n%s", p, x, y)
		}
	}
}
