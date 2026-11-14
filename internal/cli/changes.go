package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/plongitudes/tesserae/internal/tracker"
)

// writeNewlyUnblocked lists the issues a change unblocked, if there are any.
func writeNewlyUnblocked(w io.Writer, issues []tracker.Issue) {
	if len(issues) == 0 {
		return
	}
	fmt.Fprintln(w, "newly unblocked:")
	for _, issue := range issues {
		fmt.Fprint(w, "  ")
		writeLine(w, issue)
	}
}

func (a *app) statusCommand() *cobra.Command {
	var (
		reason string
		force  bool
	)
	cmd := &cobra.Command{
		Use:   "status ID... STATUS",
		Short: "Set the status of issues; prints the issues newly unblocked",
		Long: `Set the status of issues; prints the issues newly unblocked.

Statuses:
  ready            not started; shows as waiting while it is blocked or has
                   open children
  in-progress      being worked on
  pending-review   the work is finished and waits for a review
  held             set aside for now; needs -r REASON
  done             finished; refused while the issue is blocked, unless
                   --force, and while it has open children
  omit             dropped without being done; needs -r REASON; refused while
                   it has open children

waiting can't be set: it is how a ready issue shows. Nothing but the status
and its reason is changed. Several IDs are changed together or not at all.`,
		Example: `  tess status api-7kq in-progress
  tess status api-7kq pending-review
  tess status api-7kq done
  tess status api-7kq api-m2x done            # several at once
  tess status api-7kq held -r "waiting for the vendor's reply"
  tess status api-7kq omit -r "superseded by api-m2x"
  tess status api-7kq ready                   # end a hold, or reopen
  tess status api-7kq done --force            # done although blocked
  tess status api-7kq done --json`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) < 2 {
				return invalidArgument(
					"status takes one or more IDs and then a status",
					"as in `tess status ID in-progress`; statuses: "+strings.Join(tracker.Statuses, ", "))
			}
			return nil
		},
		RunE: action(func(cmd *cobra.Command, args []string) error {
			ids, status := args[:len(args)-1], args[len(args)-1]
			ctx := cmd.Context()
			return withTracker(ctx, func(tr tracker.Tracker) error {
				change, err := tr.Status(ctx, ids, status, reason, force)
				if err != nil {
					return err
				}
				return a.print(change, func(w io.Writer) {
					for _, issue := range change.Updated {
						writeLine(w, issue)
					}
					writeNewlyUnblocked(w, change.NewlyUnblocked)
				})
			})
		}),
	}
	cmd.Flags().StringVarP(&reason, "reason", "r", "", "why; held and omit need one")
	cmd.Flags().BoolVar(&force, "force", false, "mark a blocked issue done anyway")
	return cmd
}

func (a *app) retireCommand() *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use:   "retire ID...",
		Short: "Hide issues permanently; prints the issues newly unblocked",
		Long: `Hide issues permanently; prints the issues newly unblocked.

Retiring is for an issue that shouldn't have existed: a mistake, a duplicate,
a test. A retired issue is left out everywhere and blocks nothing, but it is
kept, and tess restore brings it back. Work that was dropped is tess status
ID omit instead.`,
		Example: `  tess retire api-7kq
  tess retire api-7kq -r "duplicate of api-m2x"
  tess retire api-7kq api-m2x --json`,
		Args: cobra.MinimumNArgs(1),
		RunE: action(func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			return withTracker(ctx, func(tr tracker.Tracker) error {
				change, err := tr.Retire(ctx, args, reason)
				if err != nil {
					return err
				}
				return a.print(change, func(w io.Writer) {
					for _, issue := range change.Updated {
						fmt.Fprintf(w, "retired %s %s\n", issue.ID, issue.Title)
					}
					writeNewlyUnblocked(w, change.NewlyUnblocked)
				})
			})
		}),
	}
	cmd.Flags().StringVarP(&reason, "reason", "r", "", "why; kept as a comment on each issue")
	return cmd
}

func (a *app) restoreCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "restore ID",
		Short: "Bring a retired issue back; takes the exact ID",
		Long: `Bring a retired issue back; takes the exact ID.

A retired issue is not found by part of its ID, so give the whole of it;
tess list --retired shows them.`,
		Example: `  tess restore api-7kq
  tess restore api-7kq --json`,
		Args: cobra.ExactArgs(1),
		RunE: action(func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			return withTracker(ctx, func(tr tracker.Tracker) error {
				issue, err := tr.Restore(ctx, args[0])
				if err != nil {
					return err
				}
				return a.print(issue, func(w io.Writer) {
					fmt.Fprintf(w, "restored %s %s\n", issue.ID, issue.Title)
				})
			})
		}),
	}
}

func (a *app) commentCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "comment ID TEXT",
		Short: `Add a comment to an issue; TEXT "-" reads stdin`,
		Long: `Add a comment to an issue; TEXT "-" reads stdin.

A comment is kept as written and can't be changed afterwards. Its author is
whoever runs the command.`,
		Example: `  tess comment api-7kq "Reproduced on staging; the redirect drops the query string"
  git log -3 --oneline | tess comment api-7kq -
  tess comment api-7kq "Fixed in the auth branch" --json`,
		Args: cobra.ExactArgs(2),
		RunE: action(func(cmd *cobra.Command, args []string) error {
			text, err := a.text("the comment", args[1])
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			return withTracker(ctx, func(tr tracker.Tracker) error {
				comment, err := tr.Comment(ctx, args[0], text)
				if err != nil {
					return err
				}
				return a.print(comment, func(w io.Writer) {
					fmt.Fprintf(w, "commented on %s\n", args[0])
				})
			})
		}),
	}
}
