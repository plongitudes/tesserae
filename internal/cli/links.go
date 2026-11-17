package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/plongitudes/tesserae/internal/tracker"
)

// splitLinkArgs splits `A... TYPE B...` at the link type: the issues before
// it, the type, and the issues after it.
func splitLinkArgs(name string, args []string) (left []string, wording string, right []string, err error) {
	types := "the link types are " + strings.Join(tracker.LinkWordings, ", ")
	at := -1
	for i, arg := range args {
		if !tracker.IsLinkWording(arg) {
			continue
		}
		if at >= 0 {
			return nil, "", nil, usage("%s takes one link type, and got both %s and %s", name, args[at], arg)
		}
		at = i
	}
	switch {
	case at < 0 && len(args) == 3:
		return nil, "", nil, usage("unknown link type %q; %s", args[1], types)
	case at < 0:
		return nil, "", nil, usage("%s takes issues, a link type, and issues; %s", name, types)
	case at == 0 || at == len(args)-1:
		return nil, "", nil, usage("%s takes at least one issue on each side of %s", name, args[at])
	}
	return args[:at], args[at], args[at+1:], nil
}

// linkWordings is what each wording of tracker.LinkWordings means, for the
// help of link and unlink.
const linkWordings = `Link types, as in "A TYPE B" (or "A B TYPE C D", for every pair):
  A blocks B             B is blocked until A is done or omitted
  B blocked-by A         the same link, said from B's end
  A parent-of B          B is a child of A
  B child-of A           the same link, said from B's end
  A related B            the two belong together; the order doesn't matter
  B discovered-from A    B was found while working on A
`

// linkCommand builds `link` or `unlink`: both take `A... TYPE B...` in any
// wording and print each link in its canonical direction.
func (a *app) linkCommand(name, short, long, example, done string,
	change func(tracker.Tracker, context.Context, []string, string, []string) ([]tracker.Link, error),
) *cobra.Command {
	return &cobra.Command{
		Use:     name + " A TYPE B",
		Short:   short,
		Long:    short + ".\n\n" + linkWordings + long,
		Example: example,
		Args: func(cmd *cobra.Command, args []string) error {
			_, _, _, err := splitLinkArgs(name, args)
			return err
		},
		RunE: action(func(cmd *cobra.Command, args []string) error {
			left, wording, right, err := splitLinkArgs(name, args)
			if err != nil {
				return err
			}
			return withTracker(cmd.Context(), func(tr tracker.Tracker) error {
				links, err := change(tr, cmd.Context(), left, wording, right)
				if err != nil {
					return err
				}
				return a.print(links, func(w io.Writer) {
					for _, link := range links {
						fmt.Fprintf(w, "%s %s %s %s\n", done, link.From, link.Type, link.To)
					}
				})
			})
		}),
	}
}

func (a *app) linkCmd() *cobra.Command {
	return a.linkCommand("link",
		"Link issues: "+strings.Join(tracker.LinkWordings, ", "), `
Each link with a direction can be said from either end, and is stored and
printed one way round: "A blocks B" and "A parent B". Linking two issues that
are already linked that way changes nothing. A link that would close a loop
of blocks and parent links is refused, and so is a second parent. A blocks
link between an issue and its own ancestor or descendant is refused as a
loop too: a parent already waits for its open children.

Either side can name several issues: the link type splits them, and every
issue on the left is linked to every issue on the right. If one of those
links is refused, none is made.`,
		`  tess link api-7kq blocks api-m2x
  tess link api-m2x blocked-by api-7kq            # the same link as the line above
  tess link api-7kq parent-of api-m2x
  tess link api-m2x child-of api-7kq              # the same link as the line above
  tess link api-7kq related api-m2x
  tess link api-m2x discovered-from api-7kq
  tess link api-7kq api-m2x blocks api-r4d api-t9w  # four links: each on the left blocks each on the right
  tess link api-m2x api-r4d child-of api-7kq      # two children of api-7kq
  tess link api-7kq blocks api-m2x --json`,
		"linked",
		func(tr tracker.Tracker, ctx context.Context, left []string, wording string, right []string) ([]tracker.Link, error) {
			return tr.Link(ctx, left, wording, right)
		})
}

func (a *app) unlinkCmd() *cobra.Command {
	return a.linkCommand("unlink",
		"Remove the links between issues; takes the same wordings as link", `
Either wording of a link removes it, whichever one it was made with.
Removing a link that isn't there is refused.

Either side can name several issues, as with link: the link between every
issue on the left and every issue on the right is removed, or none is.`,
		`  tess unlink api-7kq blocks api-m2x
  tess unlink api-m2x blocked-by api-7kq          # the same link as the line above
  tess unlink api-m2x child-of api-7kq
  tess unlink api-7kq related api-m2x
  tess unlink api-7kq api-m2x blocks api-r4d      # two links: each on the left no longer blocks api-r4d
  tess unlink api-7kq blocks api-m2x --json`,
		"unlinked",
		func(tr tracker.Tracker, ctx context.Context, left []string, wording string, right []string) ([]tracker.Link, error) {
			return tr.Unlink(ctx, left, wording, right)
		})
}

// writeTree writes a node as a list line, then what is under it, one step
// further in.
func writeTree(w io.Writer, node tracker.TreeNode, indent, mark string) {
	fmt.Fprintf(w, "%s%s%s %s %s\n", indent, mark, symbols[node.Status], node.ID, node.Title)
	writeTreeUnder(w, node, indent+"  ")
}

// writeTreeUnder writes what is under a node: its blockers (marked
// `blocked by`), then its children.
func writeTreeUnder(w io.Writer, node tracker.TreeNode, indent string) {
	for _, blocker := range node.BlockedBy {
		writeTree(w, blocker, indent, "blocked by ")
	}
	for _, child := range node.Children {
		writeTree(w, child, indent, "")
	}
}
