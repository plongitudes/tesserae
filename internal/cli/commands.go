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

func (a *app) createCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "create TITLE",
		Short: "Create an issue; prints its ID",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return invalidArgument(
					fmt.Sprintf("create takes one title, and got %d arguments", len(args)),
					`quote the title: tess create "Fix the login redirect"`)
			}
			return nil
		},
		RunE: action(func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			return withTracker(ctx, func(tr tracker.Tracker) error {
				issue, err := tr.Create(ctx, tracker.NewIssue{Title: args[0]})
				if err != nil {
					return err
				}
				return a.print(issue, func(w io.Writer) { fmt.Fprintln(w, issue.ID) })
			})
		}),
	}
}

func (a *app) showCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show ID...",
		Short: "Show issues in full, with their links and comments",
		Args:  cobra.MinimumNArgs(1),
		RunE: action(func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			return withTracker(ctx, func(tr tracker.Tracker) error {
				issues := make([]tracker.Issue, 0, len(args))
				for _, id := range args {
					issue, err := tr.Get(ctx, id)
					if err != nil {
						return err
					}
					issues = append(issues, issue)
				}
				var result any = issues
				if len(issues) == 1 {
					result = issues[0]
				}
				return a.print(result, func(w io.Writer) {
					for _, issue := range issues {
						writeLine(w, issue)
					}
				})
			})
		}),
	}
}

func (a *app) listCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List issues",
		Args:  cobra.NoArgs,
		RunE: action(func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			return withTracker(ctx, func(tr tracker.Tracker) error {
				issues, err := tr.List(ctx, tracker.Filter{})
				if err != nil {
					return err
				}
				return a.print(issues, func(w io.Writer) {
					for _, issue := range issues {
						writeLine(w, issue)
					}
				})
			})
		}),
	}
}

func (a *app) syncCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Share issues with other machines (not available yet)",
		Args:  cobra.NoArgs,
		RunE: action(func(cmd *cobra.Command, args []string) error {
			// With --json, stderr carries only an error object.
			if !a.json {
				fmt.Fprintln(a.stderr, "sync: not available yet")
			}
			return nil
		}),
	}
}
