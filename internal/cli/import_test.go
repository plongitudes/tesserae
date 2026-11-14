package cli

import (
	"os"
	"reflect"
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

	if got, want := ok[object](t, "import", "old.jsonl"), (object{"imported": 2.0, "skipped": 0.0}); !reflect.DeepEqual(got, want) {
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
