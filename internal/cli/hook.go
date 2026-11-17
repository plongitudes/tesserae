package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/plongitudes/tesserae/internal/tracker"
)

// hookInput is the part of Claude Code's hook JSON, given on stdin, that
// tess reads.
type hookInput struct {
	Source string `json:"source"` // SessionStart: startup, resume, compact, ...
}

// syncBeforeFacts brings the issues up to date before they are counted,
// except after a compaction, where the session is already under way and only
// needs the facts again (ADR-0019, item 4).
func (a *app) syncBeforeFacts(in hookInput) {
	if in.Source == "compact" {
		return
	}
	// Sync is a stub in loop 1 (cli.md), so there is nothing to run yet.
	// Loop 2 runs it here, with a short timeout, and stays quiet if it fails.
}

func (a *app) hookCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook session-start|session-end",
		Short: "Plumbing for the Claude Code hooks that `tess setup claude` installs",
		Long: `Plumbing for the Claude Code hooks that ` + "`tess setup claude`" + ` installs.

A hook must never get in a session's way, so these print nothing when there
is nothing to say, and exit with status 0 whatever happens.`,
		Example: `  echo '{"source":"startup"}' | tess hook session-start
  tess hook session-end`,
		Args: func(cmd *cobra.Command, args []string) error {
			return usage("hook takes the event it is run for, session-start or session-end")
		},
		RunE: func(cmd *cobra.Command, args []string) error { return nil },
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "session-start",
			Short: "Print a few facts about the issues, for the start of a session",
			Long: `Print a few facts about the issues, for the start of a session.

Reads the hook's JSON on stdin. Prints nothing where there is no database.`,
			Example: `  echo '{"source":"startup"}' | tess hook session-start`,
			Args:    cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				a.sessionStart(cmd)
				return nil
			},
		},
		&cobra.Command{
			Use:     "session-end",
			Short:   "Run at the end of a session; does nothing until sync exists",
			Example: `  tess hook session-end`,
			Args:    cobra.NoArgs,
			// Loop 2 starts a detached `tess sync` here and returns at once.
			RunE: func(cmd *cobra.Command, args []string) error { return nil },
		},
	)
	return cmd
}

// sessionStart prints the facts, whole or not at all. No database, no
// repository, or any failure on the way is silence.
func (a *app) sessionStart(cmd *cobra.Command) {
	var in hookInput
	if !isTerminal(a.stdin) { // run by hand, there is no hook JSON to wait for
		if text, err := io.ReadAll(a.stdin); err == nil {
			_ = json.Unmarshal(text, &in) // no input, or not JSON, is a plain start
		}
	}
	a.syncBeforeFacts(in)

	ctx := cmd.Context()
	var facts bytes.Buffer
	err := withTracker(ctx, func(tr tracker.Tracker) error {
		fmt.Fprintln(&facts, "This repository tracks its issues with tess.")
		for _, count := range []struct {
			label  string
			filter tracker.Filter
		}{
			{"Ready to start", tracker.Filter{Ready: true}},
			{"In progress", tracker.Filter{Status: "in-progress"}},
			{"Waiting on other issues", tracker.Filter{Status: "waiting"}},
		} {
			issues, err := tr.List(ctx, count.filter)
			if err != nil {
				return err
			}
			fmt.Fprintf(&facts, "%s: %d\n", count.label, len(issues))
		}
		fmt.Fprintln(&facts, "Last sync: never")
		return nil
	})
	if err == nil {
		a.stdout.Write(facts.Bytes())
	}
}
