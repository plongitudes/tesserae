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

// linkWordings is what each wording of tracker.LinkWordings means, for the
// help of link and unlink.
const linkWordings = `Link types, as in "A TYPE B":
  A blocks B             B is blocked until A is done or omitted
  B blocked-by A         the same link, said from B's end
  A parent-of B          B is a child of A
  B child-of A           the same link, said from B's end
  A related B            the two belong together; the order doesn't matter
  B discovered-from A    B was found while working on A
`

// linkCommand builds `link` or `unlink`: both take `A TYPE B` in any
// wording and print the link in its canonical direction.
func (a *app) linkCommand(name, short, long, example, done string, change func(tracker.Tracker, *cobra.Command, []string) (tracker.Link, error)) *cobra.Command {
	return &cobra.Command{
		Use:     name + " A TYPE B",
		Short:   short,
		Long:    short + ".\n\n" + linkWordings + long,
		Example: example,
		Args:    linkArgs(name),
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
		"Link two issues: "+strings.Join(tracker.LinkWordings, ", "), `
Each link with a direction can be said from either end, and is stored and
printed one way round: "A blocks B" and "A parent B". Linking two issues that
are already linked that way changes nothing. A link that would close a loop
of blocks and parent links is refused, and so is a second parent.`,
		`  tess link api-7kq blocks api-m2x
  tess link api-m2x blocked-by api-7kq            # the same link as the line above
  tess link api-7kq parent-of api-m2x
  tess link api-m2x child-of api-7kq              # the same link as the line above
  tess link api-7kq related api-m2x
  tess link api-m2x discovered-from api-7kq
  tess link api-7kq blocks api-m2x --json`,
		"linked",
		func(tr tracker.Tracker, cmd *cobra.Command, args []string) (tracker.Link, error) {
			return tr.Link(cmd.Context(), args[0], args[1], args[2])
		})
}

func (a *app) unlinkCmd() *cobra.Command {
	return a.linkCommand("unlink",
		"Remove the link between two issues; takes the same wordings as link", `
Either wording of a link removes it, whichever one it was made with.
Removing a link that isn't there is refused.`,
		`  tess unlink api-7kq blocks api-m2x
  tess unlink api-m2x blocked-by api-7kq          # the same link as the line above
  tess unlink api-m2x child-of api-7kq
  tess unlink api-7kq related api-m2x
  tess unlink api-7kq blocks api-m2x --json`,
		"unlinked",
		func(tr tracker.Tracker, cmd *cobra.Command, args []string) (tracker.Link, error) {
			return tr.Unlink(cmd.Context(), args[0], args[1], args[2])
		})
}

func (a *app) treeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "tree ID",
		Short: "Show an issue with its children and blockers, and theirs",
		Long: `Show an issue with its children and blockers, and theirs.

The tree runs downwards from the issue: under each issue come the issues that
block it, marked "blocked by", and then its children.`,
		Example: `  tess tree api-7kq
  tess tree api-7kq --json`,
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
