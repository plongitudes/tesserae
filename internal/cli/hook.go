package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"

	"github.com/plongitudes/tesserae/internal/sync"
	"github.com/plongitudes/tesserae/internal/tracker"
)

// hookInput is the part of Claude Code's hook JSON, given on stdin, that
// tess reads.
type hookInput struct {
	Source string `json:"source"` // SessionStart: startup, resume, compact, ...
}

// syncDeadline is how long the session-start hook lets its sync run
// (sync.md, "Hooks"): a session must not wait on a slow remote.
var syncDeadline = 10 * time.Second

// detach starts a process that outlives this one. It is a variable so the
// tests can see what would be started without starting it.
var detach = startDetached

// startDetached starts program in a session of its own, with no input and
// its output discarded, and does not wait for it.
func startDetached(program string, args ...string) error {
	cmd := exec.Command(program, args...)
	// Stdin, Stdout and Stderr are left nil, which is the null device.
	cmd.SysProcAttr = detachedAttr()
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// timedOut is what the Last sync fact says in place of a code when the
// deadline ended the sync.
const timedOut = "timed out"

// syncBeforeFacts brings the issues up to date before they are counted,
// except after a compaction, where the session is already under way and only
// needs the facts again (ADR-0019, item 4). It returns "" when the sync
// worked, and otherwise why it did not, as the Last sync fact says it: the
// error's code, or that the deadline ended it. Nothing is printed: a
// resolution is in the issue's history, and a failure is a fact.
func syncBeforeFacts(ctx context.Context, path string, now func() time.Time) string {
	ctx, cancel := context.WithTimeout(ctx, syncDeadline)
	defer cancel()
	_, err := sync.Run(ctx, sync.Options{DBPath: path, Now: now, Actor: actor(ctx)})
	var refusal *tracker.Error
	switch {
	case err == nil:
		return ""
	case ctx.Err() != nil:
		return timedOut
	case errors.As(err, &refusal):
		return refusal.Code
	}
	return codeInternal
}

// lastSync is the Last sync fact (acceptance/README.md, "Hooks"). ran says
// whether a sync was tried just now, and failed is why it did not work.
func lastSync(ctx context.Context, path string, now time.Time, ran bool, failed string) (string, error) {
	if ran && failed == "" {
		return "just now", nil
	}
	state, err := sync.ReadState(ctx, "", path)
	if err != nil {
		return "", err
	}
	never := state.LastAt.IsZero()
	switch {
	case never && (!ran || failed == sync.CodeNoRemote):
		return "never", nil
	case never:
		return fmt.Sprintf("not synced (%s)", failed), nil
	case ran:
		return fmt.Sprintf("%s, not synced now (%s)", ago(now.Sub(state.LastAt)), failed), nil
	}
	fact := ago(now.Sub(state.LastAt))
	if state.Changed > 0 {
		fact += fmt.Sprintf(", %s changed since", issueCount(state.Changed, "0"))
	}
	return fact, nil
}

// ago says how long ago something was, in one unit. Under a minute by the
// clock is "just now", and so is a time the clock has not reached.
func ago(d time.Duration) string {
	count := func(n time.Duration, unit string) string {
		if n == 1 {
			return "1 " + unit + " ago"
		}
		return fmt.Sprintf("%d %ss ago", n, unit)
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return count(d/time.Minute, "minute")
	case d < 48*time.Hour:
		return count(d/time.Hour, "hour")
	}
	return count(d/(24*time.Hour), "day")
}

func (a *app) hookCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook session-start|session-end",
		Short: "Plumbing for the Claude Code hooks that `tess setup claude` installs",
		Long: `Plumbing for the Claude Code hooks that ` + "`tess setup claude`" + ` installs.

A hook must never get in a session's way, so these print nothing when there
is nothing to say, never write to stderr, and exit with status 0 whatever
happens. session-start syncs and prints a few facts; session-end leaves a
sync running behind it.`,
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
			Short: "Sync, then print a few facts about the issues, for the start of a session",
			Long: `Sync, then print a few facts about the issues, for the start of a session.

Reads the hook's JSON on stdin. Unless its source is compact, runs the sync
first and gives it ten seconds. Then prints how many issues are ready, in
progress and waiting, and a Last sync line: "just now" after a sync that
worked, "never" for a copy with no remote, "not synced (CODE)" when the sync
failed, and "WHEN, not synced now (CODE)" when it failed on a copy that has
synced before. After a compaction the line is when the last sync was and how
many issues changed since. A sync that fails is one of the facts, never an
error; ` + "`tess sync`" + ` says more. Prints nothing where there is no database.`,
			Example: `  echo '{"source":"startup"}' | tess hook session-start`,
			Args:    cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				a.sessionStart(cmd)
				return nil
			},
		},
		&cobra.Command{
			Use:   "session-end",
			Short: "Leave a sync running at the end of a session",
			Long: `Leave a sync running at the end of a session.

Starts ` + "`tess sync`" + ` on its own, with its output discarded, and exits at once
without waiting for it. Does nothing where there is no database.`,
			Example: `  tess hook session-end`,
			Args:    cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				sessionEnd(cmd.Context())
				return nil
			},
		},
	)
	return cmd
}

// sessionEnd starts `tess sync` detached and does not wait for it. What
// the sync did is in sync.last_result, for the next session's facts.
func sessionEnd(ctx context.Context) {
	path, err := databasePath(ctx)
	if err != nil {
		return
	}
	if _, err := os.Stat(path); err != nil {
		return // no database: nothing to sync
	}
	program, err := os.Executable()
	if err != nil {
		return
	}
	_ = detach(program, "sync")
}

// sessionStart prints the facts, whole or not at all. No database, no
// repository, or any failure on the way is silence; a sync that fails is
// not such a failure but the last of the facts.
func (a *app) sessionStart(cmd *cobra.Command) {
	var in hookInput
	if !isTerminal(a.stdin) { // run by hand, there is no hook JSON to wait for
		if text, err := io.ReadAll(a.stdin); err == nil {
			_ = json.Unmarshal(text, &in) // no input, or not JSON, is a plain start
		}
	}
	ctx := cmd.Context()
	path, err := databasePath(ctx)
	if err != nil {
		return
	}
	now, err := clock()
	if err != nil {
		return
	}
	ran, failed := in.Source != "compact", ""
	if ran {
		failed = syncBeforeFacts(ctx, path, now)
	}

	var facts bytes.Buffer
	err = withTracker(ctx, func(tr tracker.Tracker) error {
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
		return nil
	})
	if err != nil {
		return
	}
	fact, err := lastSync(ctx, path, now(), ran, failed)
	if err != nil {
		return
	}
	fmt.Fprintf(&facts, "Last sync: %s\n", fact)
	a.stdout.Write(facts.Bytes())
}
