package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/plongitudes/tesserae/internal/tracker"
)

func (a *app) importCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "import PATH",
		Short: "Bring in the issues of an old bd issues.jsonl, keeping their IDs",
		Long: `Bring in the issues of an old bd issues.jsonl, keeping their IDs.

Statuses and link types are mapped to the ones tess has; comments, labels and
timestamps are carried over. Either the whole file is imported or none of it.
An issue whose ID is already here is skipped, so importing a file again adds
only what is new.`,
		Example: `  tess import .beads/issues.jsonl
  tess import ../old-checkout/.beads/issues.jsonl --json`,
		Args: cobra.ExactArgs(1),
		RunE: action(func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			return withTracker(ctx, func(tr tracker.Tracker) error {
				file, err := os.Open(args[0])
				if err != nil {
					return invalidArgument(
						fmt.Sprintf("can't read %s: %v", args[0], errors.Unwrap(err)),
						"import takes the path of bd's issues.jsonl, usually .beads/issues.jsonl")
				}
				defer file.Close()
				result, err := tr.Import(ctx, file)
				if err != nil {
					return err
				}
				return a.print(result, func(w io.Writer) {
					fmt.Fprintf(w, "imported %d, skipped %d\n", result.Imported, result.Skipped)
				})
			})
		}),
	}
}
