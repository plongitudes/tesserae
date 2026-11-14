package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

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

func (a *app) syncCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Share issues with other machines (not available yet)",
		Long: `Share issues with other machines (not available yet).

For now this does nothing and says so on stderr; it is safe to run.`,
		Example: `  tess sync`,
		Args:    cobra.NoArgs,
		RunE: action(func(cmd *cobra.Command, args []string) error {
			// With --json, stderr carries only an error object.
			if !a.json {
				fmt.Fprintln(a.stderr, "sync: not available yet")
			}
			return nil
		}),
	}
}
