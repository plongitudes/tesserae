package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// orientation is what `tess` prints with no arguments (ADR-0019, item 1).
const orientation = `tess tracks this repository's issues. One database serves every worktree.

  tess ready                    issues that can be worked on now, by priority
  tess create "Title" -p 1      add an issue; prints its ID
  tess show ID                  one issue in full, with its links and comments
  tess status ID in-progress    ready | in-progress | pending-review | held | done | omit
  tess link A blocks B          B waits until A is done or omitted
  tess list --status waiting    every other view: filters, --all, --retired
  tess sync                     share issues with other machines

Add --json to any command for stable JSON; errors carry a code and a hint.
Run ` + "`tess COMMAND --help`" + ` for flags and examples.
`

func (a *app) rootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "tess",
		Short: "Issue tracker for a git repository",
		// Errors are printed once, by run, in the shape --json asks for.
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprint(a.stdout, orientation)
			return err
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetIn(a.stdin)
	root.SetOut(a.stdout)
	root.SetErr(a.stderr)
	root.PersistentFlags().BoolVar(&a.json, "json", false, "print JSON: the result on stdout, an error object on stderr")
	root.AddCommand(
		a.initCommand(),
		a.createCommand(),
		a.showCommand(),
		a.listCommand(),
		a.syncCommand(),
	)
	return root
}
