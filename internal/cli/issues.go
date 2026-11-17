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
		return "", usage(`%s is "-", but stdin was already read for another one; only one text in a command can be "-"`, name)
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
		Long: `Create an issue; prints its ID.

The issue starts as ready. Give the title as one argument, in quotes. The
long texts (--description, --design, --acceptance, --notes) take their value
verbatim, or "-" to read it from stdin; one text per command can be "-".
With --json the whole issue is printed.`,
		Example: `  tess create "Fix the login redirect"
  tess create "Fix the login redirect" -t bug -p 1 -l auth -l urgent
  tess create "Write the migration" --parent api-7kq -b api-m2x -a sam
  tess create "Rate limiting" -t epic -d "Per-key limits on the public API"
  cat design.md | tess create "Token refresh" --design -
  tess create "Flaky upload test" -t bug --json`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usage("create takes one title, quoted if it has spaces, and got %d arguments", len(args))
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
	var (
		history, tree bool
		field         string
	)
	cmd := &cobra.Command{
		Use:   "show ID...",
		Short: "Show issues in full, with their links and comments",
		Long: `Show issues in full, with their links and comments.

With --json, one ID prints one issue object and several print an array, in
the order asked. A retired issue can be shown by its exact ID.

--tree adds what hangs off the issue: its children and the issues that block
it, nested, and theirs in turn ("tree" in the JSON).

--field F takes one ID and prints that field alone, raw: a text exactly as
stored, with no newline added; a number, a date or a status on one line; a
list one value to a line; and nothing at all for a field that is not set.
With --json it prints the field's value as JSON. F is any key of the issue
object, as --json prints it.`,
		Example: `  tess show api-7kq
  tess show 7kq                  # any part of an ID that names one issue
  tess show api-7kq api-m2x
  tess show api-7kq --history    # every change, oldest first
  tess show api-7kq --tree       # with its children and blockers, nested
  tess show api-7kq --field notes > notes.md
  tess show api-7kq --field labels --json
  tess show api-7kq --json`,
		Args: cobra.MinimumNArgs(1),
		RunE: action(func(cmd *cobra.Command, args []string) error {
			one := cmd.Flags().Changed("field")
			if one {
				if len(args) != 1 {
					return usage("--field takes one issue, and got %d", len(args))
				}
				if err := checkField(field); err != nil {
					return err
				}
			}
			var opts []tracker.GetOption
			if history || field == "history" {
				opts = append(opts, tracker.WithHistory())
			}
			if tree || field == "tree" {
				opts = append(opts, tracker.WithTree())
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
				if one {
					return a.printField(issues[0], field)
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
	cmd.Flags().BoolVar(&tree, "tree", false, "add the issue's children and blockers, nested, and theirs")
	cmd.Flags().StringVar(&field, "field", "", "print one field's raw value (one ID): any key of the issue object")
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
	return time.Time{}, usage(`--due is %q, which is not a date like 2026-02-01, a time like 2026-02-01T09:00:00Z, or "" to clear it`, value)
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
		Long: `Change any field of an issue but its status.

Only the flags given are changed. An empty value clears a field that can be
empty. The long texts take "-" to read stdin, as in create. The status is
changed with tess status, and links with tess link.

--append makes -d, --design, --acceptance and --notes add to the field
instead of replacing it: the old text, one blank line, then the new text as
given. It needs at least one of them, and does not apply to --title.`,
		Example: `  tess update api-7kq --title "Fix the login redirect loop"
  tess update api-7kq -p 0 -a sam
  tess update api-7kq --add-label auth --remove-label urgent
  tess update api-7kq --due 2026-03-01
  tess update api-7kq --due "" -a ""         # clear the due date and the assignee
  git diff --stat | tess update api-7kq --notes -
  tess update api-7kq --notes "Retried with a longer timeout; same failure" --append
  cat more.md | tess update api-7kq --design - --append`,
		Args: cobra.ExactArgs(1),
		RunE: action(func(cmd *cobra.Command, args []string) error {
			fields := 0
			for _, name := range updateFlags {
				if cmd.Flags().Changed(name) {
					fields++
				}
			}
			if fields == 0 {
				return usage("update was given nothing to change; name a field with a flag (a status is changed with `tess status`)")
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
	flags.BoolVar(&patch.Append, "append", false, "the text flags add a paragraph to the field instead of replacing it")
	flags.StringVarP(&assignee, "assignee", "a", "", `who is working on it; "" for no one`)
	flags.StringVar(&owner, "owner", "", `who answers for it; "" for no one`)
	flags.StringVar(&due, "due", "", `when it is due: YYYY-MM-DD or RFC 3339; "" for no date`)
	flags.StringArrayVar(&patch.AddLabels, "add-label", nil, "a label to add; repeat for several")
	flags.StringArrayVar(&patch.RemoveLabels, "remove-label", nil, "a label to remove; repeat for several")
	return cmd
}
