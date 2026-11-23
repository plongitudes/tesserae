package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/plongitudes/tesserae/internal/git"
	"github.com/plongitudes/tesserae/internal/sync"
	"github.com/plongitudes/tesserae/internal/tracker"
)

// The levels of a doctor check (cli.md, Output). Info is a fact that is
// not a problem; only fail makes doctor exit 1.
const (
	levelOK   = "ok"
	levelInfo = "info"
	levelWarn = "warn"
	levelFail = "fail"
)

// doctorCheck is one check of `tess doctor`, and one element of its JSON
// (acceptance/README.md, "Doctor output").
type doctorCheck struct {
	Check  string  `json:"check"`
	Level  string  `json:"level"`
	Detail string  `json:"detail"`
	Fix    *string `json:"fix"`
}

// errChecksFailed is doctor's error when a check is at fail. The report
// has said everything, so nothing more is printed.
var errChecksFailed = errors.New("a check failed")

// report is the checks made so far, in order.
type report struct {
	checks []doctorCheck
}

// add records a check. An empty fix is none.
func (r *report) add(name, level, detail, fix string) {
	c := doctorCheck{Check: name, Level: level, Detail: detail}
	if fix != "" {
		c.Fix = &fix
	}
	r.checks = append(r.checks, c)
}

func (r *report) failed() bool {
	for _, c := range r.checks {
		if c.Level == levelFail {
			return true
		}
	}
	return false
}

// write prints the report as plain text: the level, the check and the
// detail in columns, and a check's fix on a line of its own under it.
func (r *report) write(w io.Writer) {
	for _, c := range r.checks {
		fmt.Fprintf(w, "%-6s%-12s  %s\n", c.Level, c.Check, c.Detail)
		if c.Fix != nil {
			fmt.Fprintf(w, "      fix: %s\n", *c.Fix)
		}
	}
}

func (a *app) doctorCommand() *cobra.Command {
	var reach bool
	cmd := &cobra.Command{
		Use:   "doctor [--remote]",
		Short: "Check the setup: git, the database, the remote, sync and the hooks",
		Long: `Check the setup: git, the database, the remote, sync and the hooks.

Prints one line per check: ok, info, warn or fail, the check's name, and
what was found. A warn or a fail has a "fix:" line under it. An info is a
fact that is not a problem, such as a copy that has never synced. The exit
status is 1 only when a check is at fail.

The checks, in order: git, repository, database, prefix, remote, sync (when
this copy last synced, and how many issues changed since), hooks (those of
` + "`tess setup claude`" + `) and other_trackers (another issue tracker's hooks or
directory beside tess). Nothing is changed. Only --remote uses the network:
it fetches from the remote to see that it can be reached.`,
		Example: `  tess doctor
  tess doctor --remote
  tess doctor --json`,
		Args: cobra.NoArgs,
		RunE: action(func(cmd *cobra.Command, args []string) error {
			r := examine(cmd.Context(), reach)
			if err := a.print(r.checks, r.write); err != nil {
				return err
			}
			if r.failed() {
				return errChecksFailed
			}
			return nil
		}),
	}
	cmd.Flags().BoolVar(&reach, "remote", false, "also fetch from the remote, to see that it can be reached")
	return cmd
}

// examine makes the checks, in the order of sync.md, "Command surface". A
// check that the ones after it rest on stops them when it fails: without
// git or a repository there is nothing more to look at, and without a
// database there is no prefix and no sync state.
func examine(ctx context.Context, reach bool) *report {
	r := &report{checks: []doctorCheck{}}

	version, err := git.Version(ctx)
	if err != nil {
		r.add("git", levelFail, err.Error(), "install git and put it on PATH")
		return r
	}
	r.add("git", levelOK, "git "+version, "")

	commonDir, err := git.Repo{}.CommonDir(ctx)
	if err != nil {
		r.add("repository", levelFail, message(err), "run tess inside a repository, or `git init` here first")
		return r
	}
	r.add("repository", levelOK, commonDir, "")

	path := filepath.Join(commonDir, databaseName)
	state, err := sync.ReadState(ctx, "", path)
	var refusal *tracker.Error
	switch {
	case err == nil:
		r.add("database", levelOK, fmt.Sprintf("%s, schema %d, %s", path, state.Schema, issueCount(state.Issues, "0")), "")
		r.add("prefix", levelOK, state.Prefix, "")
	case errors.As(err, &refusal) && refusal.Code == tracker.CodeNotInitialized:
		r.add("database", levelFail, "none at "+path, "tess init")
	default:
		r.add("database", levelFail, fmt.Sprintf("can't read %s: %v", path, err),
			"if a newer tess wrote this database, upgrade tess")
	}

	r.remote(ctx, reach)
	if state != nil {
		r.sync(state)
	}

	root, err := git.Repo{}.WorktreeRoot(ctx)
	if err != nil {
		// A bare repository, or under .git: no settings file of its own.
		root = ""
	}
	r.hooks(root)
	r.otherTrackers(root)
	return r
}

// message is an error's text without its code or hint.
func message(err error) string {
	var refusal *tracker.Error
	if errors.As(err, &refusal) {
		return refusal.Message
	}
	return err.Error()
}

// issueCount is "1 issue", "3 issues", and none for no issues at all.
func issueCount(n int, none string) string {
	switch n {
	case 0:
		return none + " issues"
	case 1:
		return "1 issue"
	}
	return fmt.Sprintf("%d issues", n)
}

// remote checks that there is a remote a sync would use and, when asked,
// that it can be reached. An unreachable remote is a warning: the issues
// here are as usable as before.
func (r *report) remote(ctx context.Context, reach bool) {
	const name = "remote"
	remotes, err := git.Repo{}.Remotes(ctx)
	if err != nil {
		r.add(name, levelWarn, message(err), "see `git remote -v`")
		return
	}
	if len(remotes) == 0 {
		r.add(name, levelInfo, "no remote configured", "git remote add origin URL, then tess sync")
		return
	}
	chosen, err := sync.FindRemote(ctx, "", "", reach)
	var refusal *tracker.Error
	switch {
	case err == nil:
		detail := chosen
		for _, remote := range remotes {
			if remote.Name == chosen {
				detail += " " + remote.URL
			}
		}
		if reach {
			detail += ", reachable"
		}
		r.add(name, levelOK, detail, "")
	case errors.As(err, &refusal) && refusal.Code == sync.CodeNoRemote:
		names := make([]string, len(remotes))
		for i, remote := range remotes {
			names[i] = remote.Name
		}
		r.add(name, levelWarn, fmt.Sprintf("%d remotes and none is origin: %s", len(names), strings.Join(names, ", ")),
			"name the one to sync with: tess sync --remote NAME")
	case errors.As(err, &refusal) && refusal.Code == sync.CodeOffline:
		r.add(name, levelWarn, refusal.Message, "check the connection and the remote's URL (git remote -v)")
	default:
		r.add(name, levelWarn, message(err), "see `git remote -v`")
	}
}

// stuckAfter is how long a sync may hold the lock before doctor calls it
// stuck: well past the 30 seconds a second sync waits for it.
const stuckAfter = 5 * time.Minute

// runningNote says what the lock file says about a sync holding it now:
// "" when none does. stuck is true when it has held the lock longer than
// stuckAfter, which with flock means a live process that is not finishing.
func runningNote(running *sync.LockHolder, now time.Time) (note string, stuck bool) {
	if running == nil {
		return "", false
	}
	age := now.Sub(running.Since).Round(time.Second)
	if age >= stuckAfter {
		return fmt.Sprintf("a sync has been running for %s (pid %d)", age, running.PID), true
	}
	return fmt.Sprintf("a sync is running now (pid %d, for %s)", running.PID, age), false
}

// sync reports when this copy last synced, how the last try went, how
// many issues have changed since, and whether a sync is running now.
func (r *report) sync(state *sync.State) {
	const name = "sync"
	// Having no remote is the remote check's news, not a sync that failed.
	failed := state.LastResult != "" && state.LastResult != sync.ResultOK && state.LastResult != sync.CodeNoRemote
	note, stuck := runningNote(state.Running, time.Now())
	level, detail, fix := levelOK, "", ""
	switch {
	case state.LastAt.IsZero() && failed:
		level, detail, fix = levelWarn, fmt.Sprintf("never synced; the last try failed (%s)", state.LastResult), "tess sync"
	case state.LastAt.IsZero():
		level, detail = levelInfo, "never synced"
	case !state.HasLKG:
		level, fix = levelWarn, "tess sync"
		detail = "last synced " + state.LastAt.UTC().Format(time.RFC3339) +
			", but git no longer has the snapshot that sync left; the next sync merges without it"
	case failed:
		level, fix = levelWarn, "tess sync"
		detail = fmt.Sprintf("last synced %s, %s changed since; the last try failed (%s)",
			state.LastAt.UTC().Format(time.RFC3339), issueCount(state.Changed, "no"), state.LastResult)
	default:
		detail = fmt.Sprintf("last synced %s, %s changed since",
			state.LastAt.UTC().Format(time.RFC3339), issueCount(state.Changed, "no"))
	}
	if stuck {
		level = levelWarn
		fix = fmt.Sprintf("wait for it, or stop process %d if it is stuck", state.Running.PID)
	}
	if note != "" {
		detail += "; " + note
	}
	r.add(name, level, detail, fix)
}

// claudeSettings reads the hooks object of a Claude Code settings file. A
// file that isn't there has none; one that can't be read is an error.
func claudeSettings(file string) (hooks *jsonObject, err error) {
	text, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	settings, err := parseJSONObject(text)
	if err != nil {
		return nil, err
	}
	return objectIn(settings, "hooks")
}

// hooks checks for the hooks of `tess setup claude` in the repository's
// settings, and for a tess on PATH for them to run. root is the top of the
// worktree, or empty where there is none.
func (r *report) hooks(root string) {
	const name = "hooks"
	const notSetUp = "not set up; tess setup claude installs them"
	if root == "" {
		r.add(name, levelInfo, "no working tree here to hold "+settingsPath, "")
		return
	}
	hooks, err := claudeSettings(filepath.Join(root, filepath.FromSlash(settingsPath)))
	if err != nil {
		r.add(name, levelWarn, fmt.Sprintf("can't read %s: %v", settingsPath, err), "fix the file by hand, then tess setup claude")
		return
	}
	var present, absent []string
	for _, h := range agentHooks {
		var groups []any
		if hooks != nil {
			groups, _ = arrayIn(hooks, h.event)
		}
		if h.count(groups) > 0 {
			present = append(present, h.event)
		} else {
			absent = append(absent, h.event)
		}
	}
	switch {
	case len(present) == 0:
		r.add(name, levelInfo, notSetUp, "")
	case len(absent) > 0:
		r.add(name, levelWarn, fmt.Sprintf("%s has the %s hook but not %s", settingsPath,
			strings.Join(present, ", "), strings.Join(absent, ", ")), "tess setup claude")
	default:
		if _, err := exec.LookPath("tess"); err != nil {
			r.add(name, levelWarn, "set up in "+settingsPath+", but tess is not on PATH, so they do nothing",
				"put the tess binary in a directory on PATH")
			return
		}
		r.add(name, levelOK, "set up in "+settingsPath, "")
	}
}

// otherTracker matches a hook command that names bd or beads.
var otherTracker = regexp.MustCompile(`\b(bd|beads)\b`)

// otherTrackers looks for another issue tracker working beside tess: its
// hooks on the events tess uses, in the repository's Claude settings or
// the user's, or its directory at the top of the worktree. It reads those
// two files and that directory's name, and changes nothing.
func (r *report) otherTrackers(root string) {
	const name = "other_trackers"
	type file struct{ label, path string }
	var files []file
	if root != "" {
		files = append(files, file{settingsPath, filepath.Join(root, filepath.FromSlash(settingsPath))})
	}
	if home, err := os.UserHomeDir(); err == nil && home != root {
		files = append(files, file{"~/" + settingsPath, filepath.Join(home, filepath.FromSlash(settingsPath))})
	}
	var found []string
	for _, f := range files {
		hooks, err := claudeSettings(f.path)
		if err != nil {
			found = append(found, fmt.Sprintf("can't read %s to look (%v)", f.label, err))
			continue
		}
		if hooks == nil {
			continue
		}
		for _, h := range agentHooks {
			groups, _ := arrayIn(hooks, h.event)
			for _, command := range hookCommandsIn(groups) {
				if otherTracker.MatchString(command) {
					found = append(found, fmt.Sprintf("%s %s hook: %s", f.label, h.event, command))
				}
			}
		}
	}
	if root != "" {
		if info, err := os.Stat(filepath.Join(root, ".beads")); err == nil && info.IsDir() {
			found = append(found, ".beads/ directory")
		}
	}
	if len(found) == 0 {
		r.add(name, levelOK, "none found", "")
		return
	}
	r.add(name, levelWarn, strings.Join(found, "; "),
		"once its issues are in tess (tess import PATH), take the other tracker's hooks and directory out by hand")
}

// hookCommandsIn lists the command of every hook in an event's groups.
func hookCommandsIn(groups []any) []string {
	var commands []string
	for _, group := range groups {
		g, ok := group.(*jsonObject)
		if !ok {
			continue
		}
		entries, _ := g.values["hooks"].([]any)
		for _, entry := range entries {
			if hook, ok := entry.(*jsonObject); ok {
				if command, _ := hook.values["command"].(string); command != "" {
					commands = append(commands, command)
				}
			}
		}
	}
	return commands
}
