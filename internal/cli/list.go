package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/plongitudes/tesserae/internal/tracker"
)

// readyDefaultLimit is how many issues `tess ready` shows without -n.
const readyDefaultLimit = 10

// layout is how a list command was asked to lay out its plain output.
type layout struct{ flat, tree bool }

func (l *layout) flags(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&l.flat, "flat", false, "one line per issue, no headers (the default when stdout isn't a terminal)")
	cmd.Flags().BoolVar(&l.tree, "tree", false, "each issue under its ancestors (the default on a terminal)")
}

func (a *app) readyCommand() *cobra.Command {
	var l layout
	filter := tracker.Filter{Ready: true}
	cmd := &cobra.Command{
		Use:   "ready",
		Short: "List the issues that can be worked on now, by priority",
		Args:  cobra.NoArgs,
		RunE: action(func(cmd *cobra.Command, args []string) error {
			return a.list(cmd.Context(), filter, l)
		}),
	}
	cmd.Flags().IntVarP(&filter.Limit, "limit", "n", readyDefaultLimit, "show at most this many issues; 0 for all")
	l.flags(cmd)
	return cmd
}

func (a *app) listCommand() *cobra.Command {
	var l layout
	var filter tracker.Filter
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List issues, by priority; done, omit and retired issues are left out",
		Args:  cobra.NoArgs,
		RunE: action(func(cmd *cobra.Command, args []string) error {
			return a.list(cmd.Context(), filter, l)
		}),
	}
	flags := cmd.Flags()
	flags.StringVar(&filter.Status, "status", "", "only issues showing this status: "+strings.Join(tracker.DisplayedStatuses, ", "))
	flags.StringVar(&filter.Type, "type", "", "only issues of this type: "+strings.Join(tracker.IssueTypes, ", "))
	flags.StringVar(&filter.Label, "label", "", "only issues with this label")
	flags.StringVar(&filter.Assignee, "assignee", "", "only issues assigned to this person")
	flags.StringVar(&filter.Parent, "parent", "", "only descendants of this issue")
	flags.BoolVar(&filter.Blocked, "blocked", false, "only blocked issues, of any status, with their blockers")
	flags.StringVarP(&filter.Search, "search", "s", "", "only issues whose title, description, or notes contain this text")
	flags.BoolVar(&filter.All, "all", false, "include done and omit issues")
	flags.BoolVar(&filter.Retired, "retired", false, "only retired issues")
	flags.IntVarP(&filter.Limit, "limit", "n", 0, "show at most this many issues; 0 for all")
	l.flags(cmd)
	return cmd
}

// list is the one body of `ready` and `list`: the tracker selects and
// orders the issues, and the layout only changes the plain output. JSON is
// always the flat array.
func (a *app) list(ctx context.Context, filter tracker.Filter, l layout) error {
	if l.flat && l.tree {
		return invalidArgument("--flat and --tree can't both be given", "pick one; --json is always flat")
	}
	return withTracker(ctx, func(tr tracker.Tracker) error {
		issues, err := tr.List(ctx, filter)
		if err != nil {
			return err
		}
		if a.json {
			return writeJSON(a.stdout, issues)
		}
		p := painter{colour: a.tty && os.Getenv("NO_COLOR") == ""}
		if filter.Blocked {
			if p.blockers, err = tr.ActiveBlockers(ctx); err != nil {
				return err
			}
		}
		if l.tree || (a.tty && !l.flat) {
			roots, err := grouped(ctx, tr, issues)
			if err != nil {
				return err
			}
			p.writeGroups(a.stdout, roots, "")
			return nil
		}
		for _, issue := range issues {
			fmt.Fprintln(a.stdout, p.line(issue))
		}
		return nil
	})
}

// listNode is an issue in a hierarchical list: a result, or an ancestor of
// one shown as a header.
type listNode struct {
	issue    tracker.Issue
	result   bool
	children []*listNode
	placed   bool
}

// grouped arranges results under their ancestors. An ancestor that isn't a
// result itself is fetched to stand as a header. Results are taken in list
// order and each node is placed when its best result comes up, so siblings,
// and the top level, come out sorted by their best result with no sort.
func grouped(ctx context.Context, tr tracker.Tracker, results []tracker.Issue) ([]*listNode, error) {
	nodes := make(map[string]*listNode, len(results))
	for _, issue := range results {
		nodes[issue.ID] = &listNode{issue: issue, result: true}
	}
	var roots []*listNode
	for _, issue := range results {
		node := nodes[issue.ID]
		for _, ancestorID := range issue.Ancestors {
			parent, ok := nodes[ancestorID]
			if !ok {
				header, err := tr.Get(ctx, ancestorID)
				if err != nil {
					return nil, err
				}
				parent = &listNode{issue: header}
				nodes[ancestorID] = parent
			}
			if !node.placed {
				node.placed = true
				parent.children = append(parent.children, node)
			}
			node = parent
		}
		if !node.placed {
			node.placed = true
			roots = append(roots, node)
		}
	}
	return roots, nil
}

// ANSI escape codes, written directly (ADR-0020).
const (
	ansiReset = "\x1b[0m"
	ansiDim   = "\x1b[2m"
)

// statusColours are the semantic colours of the status symbols on a
// terminal; ready is left in the terminal's own colour.
var statusColours = map[string]string{
	"waiting":        ansiDim,
	"in-progress":    "\x1b[33m", // yellow
	"pending-review": "\x1b[35m", // magenta
	"held":           "\x1b[36m", // cyan
	"done":           "\x1b[32m", // green
	"omit":           ansiDim,
}

// painter writes list lines, with colour or without.
type painter struct {
	colour bool
	// blockers, when set, are named after each blocked issue: its active
	// blockers by issue ID.
	blockers map[string][]string
}

// line is a result's line: `<symbol> <id> <title>`, and in a list of
// blocked issues, what blocks it.
func (p painter) line(issue tracker.Issue) string {
	symbol := symbols[issue.Status]
	if colour := statusColours[issue.Status]; p.colour && colour != "" {
		symbol = colour + symbol + ansiReset
	}
	line := symbol + " " + issue.ID + " " + issue.Title
	if p.blockers == nil {
		return line
	}
	if blockers := p.blockers[issue.ID]; len(blockers) > 0 {
		return line + " (blocked by " + strings.Join(blockers, ", ") + ")"
	}
	// Blocked with no active blocker of its own: an ancestor is blocked
	// or held, so the parent is where to look next.
	if issue.Blocked && issue.Parent != nil {
		return line + " (blocked through " + *issue.Parent + ")"
	}
	return line
}

// writeGroups writes nodes and what is under them, two spaces further in
// per level. A header is context, not a result: it is dimmed as a whole.
func (p painter) writeGroups(w io.Writer, nodes []*listNode, indent string) {
	for _, node := range nodes {
		switch {
		case node.result:
			fmt.Fprintln(w, indent+p.line(node.issue))
		case p.colour:
			fmt.Fprintln(w, ansiDim+indent+symbols[node.issue.Status]+" "+node.issue.ID+" "+node.issue.Title+ansiReset)
		default:
			fmt.Fprintln(w, indent+symbols[node.issue.Status]+" "+node.issue.ID+" "+node.issue.Title)
		}
		p.writeGroups(w, node.children, indent+"  ")
	}
}
