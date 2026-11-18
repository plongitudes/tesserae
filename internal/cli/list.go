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
		Use:   "ready [-n N] [--flat|--tree]",
		Short: "List the issues that can be worked on now, by priority",
		Long: `List the issues that can be worked on now, by priority.

An issue is listed when its status is ready, nothing blocks it, and it has no
open children. The order is priority, then age. On a terminal each issue is
shown under its ancestors; elsewhere the output is one line per issue. When
nothing is ready, a terminal gets a note on stderr and a pipeline gets nothing.`,
		Example: `  tess ready
  tess ready -n 3
  tess ready -n 0                # all of them
  tess ready --flat | head -1
  tess ready --json`,
		Args: cobra.NoArgs,
		RunE: action(func(cmd *cobra.Command, args []string) error {
			return a.list(cmd.Context(), filter, l)
		}),
	}
	cmd.Flags().IntVarP(&filter.Limit, "limit", "n", readyDefaultLimit, "show at most `N` issues; 0 for all")
	l.flags(cmd)
	return cmd
}

func (a *app) listCommand() *cobra.Command {
	var l layout
	var filter tracker.Filter
	cmd := &cobra.Command{
		Use:   "list [--status STATUS] [--type TYPE] [--label LABEL] [--assignee NAME] [--parent ID] [--blocked] [-s TEXT] [--all] [--retired] [-n N] [--flat|--tree]",
		Short: "List issues, by priority; done, omit and retired issues are left out",
		Long: `List issues, by priority; done, omit and retired issues are left out.

The filters combine: an issue is listed when it passes all of them. --all
adds done and omit issues, and so does naming one with --status. --retired
lists the retired issues and nothing else. On a terminal each issue is shown
under its ancestors; elsewhere the output is one line per issue.`,
		Example: `  tess list
  tess list --status in-progress
  tess list --status waiting --assignee sam
  tess list --blocked                      # with what blocks each one
  tess list --parent api-7kq --all         # everything under an epic
  tess list --type bug --label auth -n 20
  tess list -s "redirect"
  tess list -s 7kq                         # -s matches IDs too
  tess list --retired
  tess list --tree | less
  tess list --all --json`,
		Args: cobra.NoArgs,
		RunE: action(func(cmd *cobra.Command, args []string) error {
			if err := checkDisplayedStatus(filter.Status); err != nil {
				return err
			}
			if filter.Type != "" {
				if err := checkType(filter.Type); err != nil {
					return err
				}
			}
			return a.list(cmd.Context(), filter, l)
		}),
	}
	flags := cmd.Flags()
	flags.StringVar(&filter.Status, "status", "", "only issues showing this `STATUS`: "+strings.Join(tracker.DisplayedStatuses, ", "))
	flags.StringVar(&filter.Type, "type", "", "only issues of this `TYPE`: "+strings.Join(tracker.IssueTypes, ", "))
	flags.StringVar(&filter.Label, "label", "", "only issues with this `LABEL`")
	flags.StringVar(&filter.Assignee, "assignee", "", "only issues assigned to `NAME`")
	flags.StringVar(&filter.Parent, "parent", "", "only descendants of the issue `ID`")
	flags.BoolVar(&filter.Blocked, "blocked", false, "only blocked issues, of any status, with their blockers")
	flags.StringVarP(&filter.Search, "search", "s", "", "only issues whose id, title, description, or notes contain `TEXT`")
	flags.BoolVar(&filter.All, "all", false, "include done and omit issues")
	flags.BoolVar(&filter.Retired, "retired", false, "only retired issues")
	flags.IntVarP(&filter.Limit, "limit", "n", 0, "show at most `N` issues; 0 for all")
	l.flags(cmd)
	return cmd
}

// list is the one body of `ready` and `list`: the tracker selects and
// orders the issues, and the layout only changes the plain output. JSON is
// always the flat array.
func (a *app) list(ctx context.Context, filter tracker.Filter, l layout) error {
	if l.flat && l.tree {
		return usage("--flat and --tree can't both be given")
	}
	return withTracker(ctx, func(tr tracker.Tracker) error {
		issues, err := tr.List(ctx, filter)
		if err != nil {
			return err
		}
		if a.json {
			return writeJSON(a.stdout, issues)
		}
		if filter.Ready && len(issues) == 0 {
			a.noReadyIssues()
			return nil
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

// noReadyIssues tells a person at a terminal that `ready` found nothing and
// where to look next. It goes to stderr, and a pipeline gets nothing at all.
func (a *app) noReadyIssues() {
	if !a.tty {
		return
	}
	fmt.Fprintln(a.stderr, "no ready issues")
	fmt.Fprintln(a.stderr, "hint: tess list shows every open issue; tess list --blocked shows what blocks them")
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
	if why := whyBlocked(issue, p.blockers); why != "" {
		return line + " (" + why + ")"
	}
	return line
}

// whyBlocked says what blocks an issue, given every issue's active blockers
// (Tracker.ActiveBlockers): those of its own, or, with none of its own, the
// parent, since an ancestor is then blocked or held and the parent is where
// to look next. It is empty for an issue that isn't blocked.
func whyBlocked(issue tracker.Issue, blockers map[string][]string) string {
	if own := blockers[issue.ID]; len(own) > 0 {
		return "blocked by " + strings.Join(own, ", ")
	}
	if issue.Blocked && issue.Parent != nil {
		return "blocked through " + *issue.Parent
	}
	return ""
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
