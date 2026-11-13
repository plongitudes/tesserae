package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/plongitudes/tesserae/internal/tracker"
)

// text returns the value of a text flag or argument: stdin, verbatim, when
// it is "-", and the value itself otherwise (cli.md). name is what to call
// it in an error.
func (a *app) text(name, value string) (string, error) {
	if value != "-" {
		return value, nil
	}
	if a.stdinRead {
		return "", invalidArgument(
			fmt.Sprintf(`%s is "-", but stdin was already read for another one`, name),
			`only one text can be "-" in a command; give the others as values`)
	}
	a.stdinRead = true
	body, err := io.ReadAll(a.stdin)
	if err != nil {
		return "", fmt.Errorf("reading stdin for %s: %w", name, err)
	}
	return string(body), nil
}

// textFlags are the four long text fields, as flags on create and update.
type textFlags struct {
	description, design, acceptance, notes string
}

func (f *textFlags) register(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.StringVarP(&f.description, "description", "d", "", `what the issue is about, or "-" to read stdin`)
	flags.StringVar(&f.design, "design", "", `how it will be done, or "-" to read stdin`)
	flags.StringVar(&f.acceptance, "acceptance", "", `acceptance criteria, or "-" to read stdin`)
	flags.StringVar(&f.notes, "notes", "", `working notes, or "-" to read stdin`)
}

// read resolves each text flag that was given, reading stdin for a "-", and
// hands it to set under the flag's name.
func (f *textFlags) read(a *app, cmd *cobra.Command, set func(flag, text string)) error {
	for _, flag := range []struct {
		name  string
		value string
	}{
		{"description", f.description}, {"design", f.design},
		{"acceptance", f.acceptance}, {"notes", f.notes},
	} {
		if !cmd.Flags().Changed(flag.name) {
			continue
		}
		text, err := a.text("--"+flag.name, flag.value)
		if err != nil {
			return err
		}
		set(flag.name, text)
	}
	return nil
}

func (a *app) createCommand() *cobra.Command {
	var (
		n        tracker.NewIssue
		priority int
		texts    textFlags
	)
	cmd := &cobra.Command{
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
			n.Title = args[0]
			if cmd.Flags().Changed("priority") {
				n.Priority = &priority
			}
			err := texts.read(a, cmd, func(flag, text string) {
				switch flag {
				case "description":
					n.Description = text
				case "design":
					n.Design = text
				case "acceptance":
					n.AcceptanceCriteria = text
				case "notes":
					n.Notes = text
				}
			})
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			return withTracker(ctx, func(tr tracker.Tracker) error {
				issue, err := tr.Create(ctx, n)
				if err != nil {
					return err
				}
				return a.print(issue, func(w io.Writer) { fmt.Fprintln(w, issue.ID) })
			})
		}),
	}
	flags := cmd.Flags()
	flags.StringVarP(&n.Type, "type", "t", "", "task (default), bug, feature, epic, or chore")
	flags.IntVarP(&priority, "priority", "p", 2, "0 (most urgent) to 4")
	texts.register(cmd)
	flags.StringArrayVarP(&n.Labels, "label", "l", nil, "a freeform label; repeat for several")
	flags.StringVar(&n.Parent, "parent", "", "create as a child of this issue")
	flags.StringArrayVarP(&n.BlockedBy, "blocked-by", "b", nil, "an issue this one waits for; repeat for several")
	flags.StringVarP(&n.Assignee, "assignee", "a", "", "who is working on it")
	return cmd
}

func (a *app) showCommand() *cobra.Command {
	var history bool
	cmd := &cobra.Command{
		Use:   "show ID...",
		Short: "Show issues in full, with their links and comments",
		Args:  cobra.MinimumNArgs(1),
		RunE: action(func(cmd *cobra.Command, args []string) error {
			var opts []tracker.GetOption
			if history {
				opts = append(opts, tracker.WithHistory())
			}
			ctx := cmd.Context()
			return withTracker(ctx, func(tr tracker.Tracker) error {
				issues := make([]tracker.Issue, 0, len(args))
				for _, id := range args {
					issue, err := tr.Get(ctx, id, opts...)
					if err != nil {
						return err
					}
					issues = append(issues, issue)
				}
				// One ID is one issue object; several are an array of them.
				var result any = issues
				if len(issues) == 1 {
					result = issues[0]
				}
				return a.print(result, func(w io.Writer) {
					for i, issue := range issues {
						if i > 0 {
							fmt.Fprintln(w)
						}
						writeDetail(w, issue)
					}
				})
			})
		}),
	}
	cmd.Flags().BoolVar(&history, "history", false, "add the issue's history: every change, oldest first")
	return cmd
}

// parseDue reads a --due value: YYYY-MM-DD, which is midnight UTC, or an
// RFC 3339 time. The empty value is the zero time, which clears the date.
func parseDue(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{time.DateOnly, time.RFC3339} {
		if at, err := time.Parse(layout, value); err == nil {
			return at.UTC(), nil
		}
	}
	return time.Time{}, invalidArgument(
		fmt.Sprintf("--due is %q, which is not a date", value),
		`give a date like 2026-02-01, a time like 2026-02-01T09:00:00Z, or "" to clear it`)
}

// updateFlags are the flags of update that name something to change.
var updateFlags = []string{
	"title", "type", "priority", "description", "design", "acceptance", "notes",
	"assignee", "owner", "due", "add-label", "remove-label",
}

func (a *app) updateCommand() *cobra.Command {
	var (
		title, issueType, assignee, owner, due string
		priority                               int
		texts                                  textFlags
		patch                                  tracker.Patch
	)
	cmd := &cobra.Command{
		Use:   "update ID",
		Short: "Change any field of an issue but its status",
		Args:  cobra.ExactArgs(1),
		RunE: action(func(cmd *cobra.Command, args []string) error {
			fields := 0
			for _, name := range updateFlags {
				if cmd.Flags().Changed(name) {
					fields++
				}
			}
			if fields == 0 {
				return invalidArgument("update was given nothing to change",
					"name a field, as in `tess update ID --title TEXT`; status changes go through `tess status`")
			}
			// Only the flags that were given go into the patch.
			for name, set := range map[string]func(){
				"title":    func() { patch.Title = &title },
				"type":     func() { patch.Type = &issueType },
				"priority": func() { patch.Priority = &priority },
				"assignee": func() { patch.Assignee = &assignee },
				"owner":    func() { patch.Owner = &owner },
			} {
				if cmd.Flags().Changed(name) {
					set()
				}
			}
			if cmd.Flags().Changed("due") {
				at, err := parseDue(due)
				if err != nil {
					return err
				}
				patch.DueAt = &at
			}
			err := texts.read(a, cmd, func(flag, text string) {
				switch flag {
				case "description":
					patch.Description = &text
				case "design":
					patch.Design = &text
				case "acceptance":
					patch.AcceptanceCriteria = &text
				case "notes":
					patch.Notes = &text
				}
			})
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			return withTracker(ctx, func(tr tracker.Tracker) error {
				issue, err := tr.Update(ctx, args[0], patch)
				if err != nil {
					return err
				}
				return a.print(issue, func(w io.Writer) { writeLine(w, issue) })
			})
		}),
	}
	flags := cmd.Flags()
	flags.StringVar(&title, "title", "", "a new title")
	flags.StringVarP(&issueType, "type", "t", "", "task, bug, feature, epic, or chore")
	flags.IntVarP(&priority, "priority", "p", 2, "0 (most urgent) to 4")
	texts.register(cmd)
	flags.StringVarP(&assignee, "assignee", "a", "", `who is working on it; "" for no one`)
	flags.StringVar(&owner, "owner", "", `who answers for it; "" for no one`)
	flags.StringVar(&due, "due", "", `when it is due: YYYY-MM-DD or RFC 3339; "" for no date`)
	flags.StringArrayVar(&patch.AddLabels, "add-label", nil, "a label to add; repeat for several")
	flags.StringArrayVar(&patch.RemoveLabels, "remove-label", nil, "a label to remove; repeat for several")
	return cmd
}
