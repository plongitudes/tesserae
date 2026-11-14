package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/plongitudes/tesserae/internal/tracker"
)

// linkArgs checks that a link command was given `A TYPE B`.
func linkArgs(name string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != 3 {
			return invalidArgument(
				name+" takes an issue, a link type, and an issue",
				"as in `tess "+name+" A blocks B`; link types: "+strings.Join(tracker.LinkWordings, ", "))
		}
		return nil
	}
}

// linkCommand builds `link` or `unlink`: both take `A TYPE B` in any
// wording and print the link in its canonical direction.
func (a *app) linkCommand(name, short, done string, change func(tracker.Tracker, *cobra.Command, []string) (tracker.Link, error)) *cobra.Command {
	return &cobra.Command{
		Use:   name + " A TYPE B",
		Short: short,
		Args:  linkArgs(name),
		RunE: action(func(cmd *cobra.Command, args []string) error {
			return withTracker(cmd.Context(), func(tr tracker.Tracker) error {
				link, err := change(tr, cmd, args)
				if err != nil {
					return err
				}
				return a.print(link, func(w io.Writer) {
					fmt.Fprintf(w, "%s %s %s %s\n", done, link.From, link.Type, link.To)
				})
			})
		}),
	}
}

func (a *app) linkCmd() *cobra.Command {
	return a.linkCommand("link",
		"Link two issues: "+strings.Join(tracker.LinkWordings, ", "), "linked",
		func(tr tracker.Tracker, cmd *cobra.Command, args []string) (tracker.Link, error) {
			return tr.Link(cmd.Context(), args[0], args[1], args[2])
		})
}

func (a *app) unlinkCmd() *cobra.Command {
	return a.linkCommand("unlink",
		"Remove the link between two issues; takes the same wordings as link", "unlinked",
		func(tr tracker.Tracker, cmd *cobra.Command, args []string) (tracker.Link, error) {
			return tr.Unlink(cmd.Context(), args[0], args[1], args[2])
		})
}

func (a *app) treeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "tree ID",
		Short: "Show an issue with its children and blockers, and theirs",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return invalidArgument("tree takes one issue ID", "as in `tess tree ID`")
			}
			return nil
		},
		RunE: action(func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			return withTracker(ctx, func(tr tracker.Tracker) error {
				root, err := tr.Tree(ctx, args[0])
				if err != nil {
					return err
				}
				return a.print(root, func(w io.Writer) { writeTree(w, root, "", "") })
			})
		}),
	}
}

// writeTree writes a node as a list line, then its blockers (marked
// `blocked by`) and its children, each one step further in.
func writeTree(w io.Writer, node tracker.TreeNode, indent, mark string) {
	fmt.Fprintf(w, "%s%s%s %s %s\n", indent, mark, symbols[node.Status], node.ID, node.Title)
	for _, blocker := range node.BlockedBy {
		writeTree(w, blocker, indent+"  ", "blocked by ")
	}
	for _, child := range node.Children {
		writeTree(w, child, indent+"  ", "")
	}
}
