package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// orientation is what `tess` prints with no arguments (ADR-0019, item 1):
// one line per working command. The commands run once per repository or by
// a hook (init, import, setup, hook) are left to `tess --help`.
const orientation = `tess tracks this repository's issues. One database serves every worktree.

  tess ready                    issues that can be worked on now, by priority
  tess create "Title" -p 1      add an issue; prints its ID
  tess show ID                  one issue in full; --tree, --history, --field F
  tess update ID --notes "..."  change fields; --append adds to a text
  tess status ID in-progress    or ready, pending-review, held, done, omit
  tess comment ID "Text"        add a comment
  tess link A blocks B          B waits for A; also parent-of, related
  tess unlink A blocks B        remove a link
  tess list --status waiting    every other view: filters, -s, --all, --retired
  tess retire ID                take a mistaken issue out of every view
  tess restore ID               bring a retired issue back
  tess sync                     share issues with other machines

Add --json to any command for stable JSON; errors carry a code and a hint.
Run ` + "`tess COMMAND --help`" + ` for flags and examples.
`

func (a *app) rootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "tess [COMMAND]",
		Short: "Issue tracker for a git repository",
		Long: `Issue tracker for a git repository.

The issues live in one database in git's common directory, shared by every
worktree. An issue that is ready can be worked on now; one that is blocked,
or has open children, shows as waiting until that clears.

Every command takes --json. An ID can be shortened to any part of it that
names one issue.`,
		Example: `  tess init --prefix api
  tess create "Fix the login redirect" -t bug -p 1
  tess ready
  tess status api-7kq in-progress
  tess link api-7kq blocks api-m2x
  tess status api-7kq done
  tess list --status waiting --json`,
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
		a.updateCommand(),
		a.statusCommand(),
		a.retireCommand(),
		a.restoreCommand(),
		a.commentCommand(),
		a.linkCmd(),
		a.unlinkCmd(),
		a.readyCommand(),
		a.listCommand(),
		a.importCommand(),
		a.syncCommand(),
		a.setupCommand(),
		a.hookCommand(),
	)
	return root
}
