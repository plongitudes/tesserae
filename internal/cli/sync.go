package cli

import (
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"github.com/plongitudes/tesserae/internal/sync"
)

// syncResult is sync's JSON (acceptance/README.md, "Sync output").
type syncResult struct {
	Remote      string           `json:"remote"`
	Fetched     int              `json:"fetched"`
	Pushed      int              `json:"pushed"`
	DryRun      bool             `json:"dry_run"`
	Initialized bool             `json:"initialized"`
	Resolved    []syncResolution `json:"resolved"`
}

type syncResolution struct {
	Issue     string  `json:"issue"`
	Field     string  `json:"field"`
	Kept      any     `json:"kept"`
	Dropped   any     `json:"dropped"`
	Won       string  `json:"won"`
	Reason    string  `json:"reason"`
	KeptAt    *string `json:"kept_at"`
	DroppedAt *string `json:"dropped_at"`
}

func (a *app) syncCommand() *cobra.Command {
	var remote string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "sync [--remote NAME] [--dry-run]",
		Short: "Exchange issues with the other copies through a git remote",
		Long: `Exchange issues with the other copies through a git remote.

Fetches the issues the remote holds, merges them with this copy's, pushes the
result and then writes it here. Where two copies changed the same field of
an issue, the later change wins and a line on stderr says which was kept
and which dropped; both stay in the issue's history. The last line says how
many issues came in and went out, or "up to date".

The remote is origin, or the only remote when there is one. The issues
travel on a ref of their own: no branch, commit or file of the working tree
is touched. A sync that fails changes nothing; run it again.

A fresh clone needs no init: when this copy has no database and the remote
has issues, the first sync creates the database with the remote's prefix.`,
		Example: `  tess sync
  tess sync --dry-run
  tess sync --remote backup
  tess sync --json`,
		Args: cobra.NoArgs,
		RunE: action(func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			path, err := databasePath(ctx)
			if err != nil {
				return err
			}
			now, err := clock()
			if err != nil {
				return err
			}
			res, err := sync.Run(ctx, sync.Options{
				DBPath: path, Remote: remote, DryRun: dryRun, Now: now, Actor: actor(ctx),
			})
			if err != nil {
				return err
			}
			out := syncResult{
				Remote: res.Remote, Fetched: res.Fetched, Pushed: res.Pushed, DryRun: res.DryRun,
				Initialized: res.Initialized, Resolved: []syncResolution{},
			}
			for _, r := range res.Resolved {
				out.Resolved = append(out.Resolved, syncResolution{
					Issue: r.Issue, Field: r.Field, Kept: r.Kept, Dropped: r.Dropped, Won: r.Won, Reason: r.Reason,
					KeptAt: eventTime(r.KeptAt), DroppedAt: eventTime(r.DroppedAt),
				})
			}
			return a.print(out, func(w io.Writer) {
				if res.Initialized {
					fmt.Fprintln(w, initialisedLine(path, res.Prefix))
				}
				for _, r := range res.Resolved {
					fmt.Fprintln(a.stderr, resolutionLine(r))
				}
				fmt.Fprintln(w, res.Summary())
			})
		}),
	}
	cmd.Flags().StringVar(&remote, "remote", "", "the git remote `NAME` to sync with (default: origin, else the only remote)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "fetch, merge and report; push nothing and change nothing here")
	return cmd
}

// initialisedLine is the first line of a sync or import that created the
// database (acceptance/README.md, "Sync output" and "Command results").
func initialisedLine(path, prefix string) string {
	return fmt.Sprintf("initialised %s with prefix %s", path, prefix)
}

// eventTime is a resolution's time as JSON carries it: null for the zero
// time, which is a side that had no event.
func eventTime(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

// resolutionLine is one resolution as plain output prints it (sync.md,
// "Command surface").
func resolutionLine(r sync.Resolution) string {
	switch r.Reason {
	case sync.ReasonCycle:
		return fmt.Sprintf("dropped %s %s %v: would make a dependency loop", r.Issue, r.Field, r.Dropped)
	case sync.ReasonRetired:
		line := fmt.Sprintf("dropped %s %s", r.Issue, r.Field)
		if collectionFields[r.Field] {
			// Which element: the one the edit added or took away.
			element := r.Dropped
			if element == nil {
				element = r.Kept
			}
			line += fmt.Sprintf(" %v", element)
		}
		line += ": issue was retired"
		if at := eventTime(r.KeptAt); at != nil {
			line += " at " + *at
		}
		return line
	}
	kept, dropped := "this copy", "other copy"
	if r.Won == sync.WonRemote {
		kept, dropped = dropped, kept
	}
	return fmt.Sprintf("resolved %s %s: kept %s, dropped %s", r.Issue, r.Field,
		sideValue(r.Field, r.Kept, kept, r.KeptAt), sideValue(r.Field, r.Dropped, dropped, r.DroppedAt))
}

// collectionFields are the units whose resolutions name one element.
var collectionFields = map[string]bool{"labels": true, "blocked_by": true, "related": true, "discovered_from": true}

// quotedFields are the units whose values are a person's own words, and are
// printed in quotes; an ID, a status or a number is printed bare.
var quotedFields = map[string]bool{"title": true, "assignee": true, "owner": true, "labels": true}

// sideValue is one side of a resolution: the value, whose copy it was, and
// when that copy last changed it.
func sideValue(field string, value any, copy string, at time.Time) string {
	var text string
	switch v := value.(type) {
	case nil:
		text = "none"
	case string:
		switch {
		case quotedFields[field]:
			text = fmt.Sprintf("%q", v)
		case slices.Contains(textFields, field) && len(v) > 12:
			// The hash of a text (ADR-0014): the start of it, as git shortens
			// a commit.
			text = "text " + v[:12]
		default:
			text = v
		}
	default:
		text = fmt.Sprint(v)
	}
	if when := eventTime(at); when != nil {
		copy += ", " + *when
	}
	return fmt.Sprintf("%s (%s)", text, copy)
}
