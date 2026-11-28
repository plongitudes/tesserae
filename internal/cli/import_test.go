package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const oldIssues = `{"id":"bd-a1","title":"Old epic","status":"open","priority":1,"issue_type":"epic","created_at":"2025-06-01T10:00:00Z","created_by":"olduser","updated_at":"2025-06-02T10:00:00Z"}
{"id":"bd-a1.1","title":"Old child","status":"closed","close_reason":"Fixed","priority":2,"issue_type":"task","created_at":"2025-06-01T10:05:00Z","updated_at":"2025-06-01T10:05:00Z","dependencies":[{"issue_id":"bd-a1.1","depends_on_id":"bd-a1","type":"parent-child"}]}
`

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestImportReadsTheFileAndPrintsTheCounts(t *testing.T) {
	inInitialisedRepo(t)
	writeFile(t, "old.jsonl", oldIssues)

	if got, want := ok[object](t, "import", "old.jsonl"), (object{"imported": 2.0, "skipped": 0.0, "initialized": false}); !reflect.DeepEqual(got, want) {
		t.Errorf("import printed %v, want %v", got, want)
	}
	child := ok[object](t, "show", "bd-a1.1")
	if child["parent"] != "bd-a1" || child["status"] != "done" || child["status_reason"] != "Fixed" {
		t.Errorf("imported child reads %v", child)
	}

	r := tess(t, "import", "old.jsonl")
	if want := "imported 0, skipped 2\n"; r.code != 0 || r.stdout != want {
		t.Errorf("re-import: exit %d, stdout %q, want %q (%s)", r.code, r.stdout, want, r.stderr)
	}
}

func TestImportWithoutADatabaseCreatesOneWithTheIDsPrefix(t *testing.T) {
	inRepo(t, "repo")
	writeFile(t, "old.jsonl", oldIssues)
	got := ok[object](t, "import", "old.jsonl")
	if got["initialized"] != true || got["imported"] != 2.0 {
		t.Errorf("import printed %v", got)
	}
	if prefix := ok[[]object](t, "doctor")[3]["detail"]; prefix != "bd" {
		t.Errorf("the prefix is %v, want bd", prefix)
	}
	again := ok[object](t, "import", "old.jsonl")
	if again["initialized"] != false || again["skipped"] != 2.0 {
		t.Errorf("re-import printed %v", again)
	}
}

func TestImportWithoutADatabaseTakesPrefixAndSaysSo(t *testing.T) {
	dir := inRepo(t, "repo")
	writeFile(t, "old.jsonl", oldIssues)
	r := tess(t, "import", "old.jsonl", "--prefix", "proj")
	path := filepath.Join(dir, ".git", "tess.db")
	if want := "initialised " + path + " with prefix proj\nimported 2, skipped 0\n"; r.code != 0 || r.stdout != want {
		t.Errorf("exit %d, stdout %q, want %q (%s)", r.code, r.stdout, want, r.stderr)
	}
	if id := ok[object](t, "create", "New")["id"]; !strings.HasPrefix(id.(string), "proj-") {
		t.Errorf("a new issue is %v, want the proj prefix", id)
	}
}

func TestImportWithoutADatabaseLowercasesAnUppercasePrefix(t *testing.T) {
	inRepo(t, "repo")
	writeFile(t, "upper.jsonl", `{"id":"SR-014","title":"Upper","status":"open","priority":2,"issue_type":"task","created_at":"2025-06-01T10:00:00Z","updated_at":"2025-06-01T10:00:00Z"}`+"\n")
	ok[object](t, "import", "upper.jsonl")
	if prefix := ok[[]object](t, "doctor")[3]["detail"]; prefix != "sr" {
		t.Errorf("the prefix is %v, want sr", prefix)
	}
	if id := ok[object](t, "show", "SR-014")["id"]; id != "SR-014" {
		t.Errorf("the imported ID reads %v", id)
	}
}

func TestImportRefusesIDsWithNoOnePrefixAndCreatesNothing(t *testing.T) {
	inRepo(t, "repo")
	writeFile(t, "mixed.jsonl", `{"id":"aa-1","title":"A","priority":2}
{"id":"bb-2","title":"B","priority":2}
`)
	r := tess(t, "import", "mixed.jsonl", "--json")
	wantError(t, r, "invalid_argument")
	if !strings.Contains(r.stderr, "--prefix") {
		t.Errorf("the hint does not say --prefix: %s", r.stderr)
	}
	if _, err := os.Stat(filepath.Join(".git", "tess.db")); err == nil {
		t.Error("a refused import made a database")
	}
	if got := ok[object](t, "import", "mixed.jsonl", "--prefix", "m"); got["imported"] != 2.0 {
		t.Errorf("with --prefix, import printed %v", got)
	}
}

func TestImportPrefixIsRefusedWhereThereIsADatabase(t *testing.T) {
	inInitialisedRepo(t)
	writeFile(t, "old.jsonl", oldIssues)
	wantError(t, tess(t, "import", "old.jsonl", "--prefix", "other", "--json"), "invalid_argument")
	if got := ok[object](t, "import", "old.jsonl"); got["imported"] != 2.0 {
		t.Errorf("without --prefix, import printed %v", got)
	}
}

func TestImportTakesADirectoryHoldingTheFile(t *testing.T) {
	inInitialisedRepo(t)
	if err := os.MkdirAll(filepath.Join("old", ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join("old", ".beads", "issues.jsonl"), oldIssues)
	if got := ok[object](t, "import", "old"); got["imported"] != 2.0 {
		t.Errorf("import of the repository printed %v", got)
	}
	if got := ok[object](t, "import", filepath.Join("old", ".beads")); got["skipped"] != 2.0 {
		t.Errorf("import of the .beads directory printed %v", got)
	}
	if err := os.Mkdir("empty", 0o755); err != nil {
		t.Fatal(err)
	}
	r := tess(t, "import", "empty", "--json")
	wantError(t, r, "invalid_argument")
	if !strings.Contains(r.stderr, filepath.Join("empty", ".beads", "issues.jsonl")) {
		t.Errorf("the refusal does not name the file it looked for: %s", r.stderr)
	}
}

func TestImportRefusalsKeepTheirCodes(t *testing.T) {
	inInitialisedRepo(t)
	wantError(t, tess(t, "import", "missing.jsonl", "--json"), "invalid_argument")
	wantError(t, tess(t, "import", "--json"), "invalid_argument")

	writeFile(t, "cycle.jsonl", `{"id":"bd-x","title":"X","priority":2,"dependencies":[{"depends_on_id":"bd-y","type":"blocks"}]}
{"id":"bd-y","title":"Y","priority":2,"dependencies":[{"depends_on_id":"bd-x","type":"blocks"}]}
`)
	wantError(t, tess(t, "import", "cycle.jsonl", "--json"), "cycle")
	wantError(t, tess(t, "show", "bd-x", "--json"), "not_found")
}
