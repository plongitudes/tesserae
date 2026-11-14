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
		Args:  cobra.ExactArgs(1),
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
