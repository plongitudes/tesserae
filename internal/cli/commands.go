package cli

import (
	"fmt"
	"io"

	"github.com/plongitudes/tesserae/internal/tracker"
)

// symbols are the status symbols of cli.md, by displayed status.
var symbols = map[string]string{
	"ready":          "○",
	"waiting":        "⊖",
	"in-progress":    "◐",
	"pending-review": "≈",
	"held":           "❄",
	"done":           "✓",
	"omit":           "✗",
}

// writeLine writes an issue as a flat list line: `<symbol> <id> <title>`.
func writeLine(w io.Writer, issue tracker.Issue) {
	fmt.Fprintf(w, "%s %s %s\n", symbols[issue.Status], issue.ID, issue.Title)
}
