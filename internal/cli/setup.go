package cli

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/plongitudes/tesserae/internal/tracker"
)

// What `tess setup claude` keeps in a worktree (ADR-0019, items 3 to 6).
const (
	skillPath    = ".claude/skills/tess/SKILL.md"
	settingsPath = ".claude/settings.json"
	allowRule    = "Bash(tess *)"
)

// codeSetupNeeded is the code of a failed `setup claude --check`.
const codeSetupNeeded = "setup_needed"

//go:embed skill.md
var skillText []byte

// agentHook is one hook tess keeps in the settings: a group under an event
// that runs `tess hook ...`.
type agentHook struct {
	event   string
	matcher string // which sessions it runs for; none means all
	run     string // the tess command; also how tess knows the hook is its own
}

var agentHooks = []agentHook{
	{event: "SessionStart", matcher: "startup|resume|compact", run: "tess hook session-start"},
	{event: "SessionEnd", run: "tess hook session-end"},
}

// group is the hook as it stands in the settings. The command is wrapped so
// that a machine without tess starts and ends its sessions quietly.
func (h agentHook) group() *jsonObject {
	hook := newJSONObject()
	hook.set("type", "command")
	hook.set("command", "command -v tess >/dev/null 2>&1 && "+h.run+" || true")
	group := newJSONObject()
	if h.matcher != "" {
		group.set("matcher", h.matcher)
	}
	group.set("hooks", []any{hook})
	return group
}

// isOurs reports whether one entry of a group's hooks runs this tess command.
func (h agentHook) isOurs(entry any) bool {
	hook, ok := entry.(*jsonObject)
	if !ok {
		return false
	}
	command, _ := hook.values["command"].(string)
	return strings.Contains(command, h.run)
}

// count is how many hook entries in an event's groups are this hook.
func (h agentHook) count(groups []any) int {
	n := 0
	for _, group := range groups {
		if g, ok := group.(*jsonObject); ok {
			entries, _ := g.values["hooks"].([]any)
			for _, entry := range entries {
				if h.isOurs(entry) {
					n++
				}
			}
		}
	}
	return n
}

// strip takes this hook out of an event's groups, and with it any group
// that held nothing else. Other people's hooks and groups stay.
func (h agentHook) strip(groups []any) []any {
	kept := []any{}
	for _, group := range groups {
		g, ok := group.(*jsonObject)
		if !ok {
			kept = append(kept, group)
			continue
		}
		entries, _ := g.values["hooks"].([]any)
		others := []any{}
		for _, entry := range entries {
			if !h.isOurs(entry) {
				others = append(others, entry)
			}
		}
		switch {
		case len(others) == len(entries):
			kept = append(kept, group)
		case len(others) > 0:
			g.set("hooks", others)
			kept = append(kept, group)
		}
	}
	return kept
}

// objectIn returns the object under key, or nil when there is none.
func objectIn(o *jsonObject, key string) (*jsonObject, error) {
	switch v := o.values[key].(type) {
	case nil:
		return nil, nil
	case *jsonObject:
		return v, nil
	}
	return nil, fmt.Errorf("%q is not an object", key)
}

// arrayIn returns the array under key, or nil when there is none.
func arrayIn(o *jsonObject, key string) ([]any, error) {
	switch v := o.values[key].(type) {
	case nil:
		return nil, nil
	case []any:
		return v, nil
	}
	return nil, fmt.Errorf("%q is not an array", key)
}

// objectFor is objectIn, making the object when there is none.
func objectFor(o *jsonObject, key string) (*jsonObject, error) {
	child, err := objectIn(o, key)
	if child == nil && err == nil {
		child = newJSONObject()
		o.set(key, child)
	}
	return child, err
}

// installSettings puts the allow rule and the hooks into the settings and
// reports whether anything had to change. A hook that is there but not as
// tess writes it now, or there twice, is replaced by the one current group.
func installSettings(settings *jsonObject) (changed bool, err error) {
	permissions, err := objectFor(settings, "permissions")
	if err != nil {
		return false, err
	}
	allow, err := arrayIn(permissions, "allow")
	if err != nil {
		return false, err
	}
	if !containsJSON(allow, allowRule) {
		permissions.set("allow", append(allow, allowRule))
		changed = true
	}

	hooks, err := objectFor(settings, "hooks")
	if err != nil {
		return false, err
	}
	for _, h := range agentHooks {
		groups, err := arrayIn(hooks, h.event)
		if err != nil {
			return false, err
		}
		if h.count(groups) == 1 && containsJSON(groups, h.group()) {
			continue
		}
		hooks.set(h.event, append(h.strip(groups), h.group()))
		changed = true
	}
	return changed, nil
}

// removeSettings takes the allow rule and the hooks out of the settings and
// reports whether anything was there. A container is dropped only when
// taking tess out of it left it empty.
func removeSettings(settings *jsonObject) (changed bool, err error) {
	permissions, err := objectIn(settings, "permissions")
	if err != nil {
		return false, err
	}
	if permissions != nil {
		allow, err := arrayIn(permissions, "allow")
		if err != nil {
			return false, err
		}
		if containsJSON(allow, allowRule) {
			others := []any{}
			for _, rule := range allow {
				if rule != allowRule {
					others = append(others, rule)
				}
			}
			setOrDrop(permissions, "allow", others)
			dropIfEmpty(settings, "permissions", permissions)
			changed = true
		}
	}

	hooks, err := objectIn(settings, "hooks")
	if err != nil {
		return false, err
	}
	if hooks != nil {
		for _, h := range agentHooks {
			groups, err := arrayIn(hooks, h.event)
			if err != nil {
				return false, err
			}
			if h.count(groups) == 0 {
				continue
			}
			setOrDrop(hooks, h.event, h.strip(groups))
			dropIfEmpty(settings, "hooks", hooks)
			changed = true
		}
	}
	return changed, nil
}

func setOrDrop(o *jsonObject, key string, array []any) {
	if len(array) == 0 {
		o.delete(key)
		return
	}
	o.set(key, array)
}

func dropIfEmpty(o *jsonObject, key string, child *jsonObject) {
	if len(child.keys) == 0 {
		o.delete(key)
	}
}

func containsJSON(array []any, value any) bool {
	for _, element := range array {
		if jsonEqual(element, value) {
			return true
		}
	}
	return false
}

// fileChange is what one run of setup would do to one file.
type fileChange struct {
	path    string // relative to the top of the worktree, with slashes
	missing bool   // the file isn't there now
	changed bool   // the file is not as this run wants it
	content []byte // what to write when changed; nil to delete the file
}

// readFile reads a file that may not be there.
func readFile(root, path string) (text []byte, missing bool, err error) {
	text, err = os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, true, nil
	}
	return text, false, err
}

// skillChange plans the skill file: written whole, or deleted.
func skillChange(root string, remove bool) (fileChange, error) {
	text, missing, err := readFile(root, skillPath)
	if err != nil {
		return fileChange{}, err
	}
	change := fileChange{path: skillPath, missing: missing}
	if remove {
		change.changed = !missing
		return change, nil
	}
	change.changed = missing || !bytes.Equal(text, skillText)
	change.content = skillText
	return change, nil
}

// settingsChange plans the settings file: tess's entries merged in or taken
// out, everything else as it was. A file left with nothing in it by a
// removal is deleted.
func settingsChange(root string, remove bool) (fileChange, error) {
	text, missing, err := readFile(root, settingsPath)
	if err != nil {
		return fileChange{}, err
	}
	change := fileChange{path: settingsPath, missing: missing}
	if missing && remove {
		return change, nil
	}
	settings, err := parseJSONObject(text)
	if err == nil {
		if remove {
			change.changed, err = removeSettings(settings)
		} else {
			change.changed, err = installSettings(settings)
		}
	}
	if err != nil {
		return fileChange{}, invalidArgument(
			fmt.Sprintf("can't read %s as Claude Code settings: %v", settingsPath, err),
			"fix the file by hand, then run this again; tess has changed nothing in it")
	}
	if !remove || len(settings.keys) > 0 {
		change.content = formatJSON(settings)
	}
	return change, nil
}

// apply writes or deletes the file. A deletion also takes away the
// directories it leaves empty, up to the top of the worktree.
func (c fileChange) apply(root string) error {
	name := filepath.Join(root, filepath.FromSlash(c.path))
	if c.content != nil {
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			return err
		}
		return os.WriteFile(name, c.content, 0o644)
	}
	if err := os.Remove(name); err != nil {
		return err
	}
	for dir := filepath.Dir(name); dir != root; dir = filepath.Dir(dir) {
		if os.Remove(dir) != nil { // not empty
			break
		}
	}
	return nil
}

// setupResult is `setup claude`'s JSON (acceptance/README.md, "Command
// results"); removeResult is the same for --remove.
type setupResult struct {
	Written   []string `json:"written"`
	Unchanged []string `json:"unchanged"`
}

type removeResult struct {
	Removed   []string `json:"removed"`
	Unchanged []string `json:"unchanged"`
}

func (a *app) setupCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup claude [--check|--remove]",
		Short: "Write the files that let a coding agent use tess",
		Long: `Write the files that let a coding agent use tess.

Claude Code is the one agent there is a setup for; see tess setup claude --help.`,
		Example: `  tess setup claude
  tess setup claude --check
  tess setup claude --remove`,
		Args: func(cmd *cobra.Command, args []string) error {
			return usage("setup takes what to set up")
		},
		RunE: func(cmd *cobra.Command, args []string) error { return nil },
	}
	cmd.AddCommand(a.setupClaudeCommand())
	return cmd
}

func (a *app) setupClaudeCommand() *cobra.Command {
	var check, remove bool
	cmd := &cobra.Command{
		Use:   "claude [--check|--remove]",
		Short: "Write the Claude Code skill, hooks and allow rule for tess",
		Long: `Write the Claude Code skill, hooks and allow rule for tess.

It writes two files at the top of the worktree, meant to be committed:

  .claude/skills/tess/SKILL.md   how to work with tess, loaded when needed
  .claude/settings.json          a Bash(tess *) allow rule, and SessionStart and
                                 SessionEnd hooks that run ` + "`tess hook ...`" + `

Everything else in settings.json is kept. Running it again changes nothing
unless a file is missing or out of date.`,
		Example: `  tess setup claude
  tess setup claude --check     # exit status 1 if anything is missing or out of date
  tess setup claude --remove    # take out only what tess added`,
		Args: cobra.NoArgs,
		RunE: action(func(cmd *cobra.Command, args []string) error {
			if check && remove {
				return usage("--check and --remove can't be used together")
			}
			root, err := worktreeRoot(cmd.Context())
			if err != nil {
				return err
			}
			// Both files are planned before either is touched, so a
			// settings file that can't be read stops the run whole.
			var changes []fileChange
			for _, plan := range []func(string, bool) (fileChange, error){skillChange, settingsChange} {
				change, err := plan(root, remove)
				if err != nil {
					return err
				}
				changes = append(changes, change)
			}
			changed, unchanged := []string{}, []string{}
			var problems []string
			for _, c := range changes {
				switch {
				case !c.changed:
					unchanged = append(unchanged, c.path)
				case c.missing:
					changed = append(changed, c.path)
					problems = append(problems, c.path+" is missing")
				default:
					changed = append(changed, c.path)
					problems = append(problems, c.path+" is out of date")
				}
			}
			if check {
				if len(problems) > 0 {
					return &tracker.Error{
						Code:    codeSetupNeeded,
						Message: strings.Join(problems, "; "),
						Hint:    "run `tess setup claude` to write what is missing or out of date",
					}
				}
				return a.print(setupResult{Written: changed, Unchanged: unchanged}, func(w io.Writer) {
					writePaths(w, "current", unchanged)
				})
			}
			for _, c := range changes {
				if c.changed {
					if err := c.apply(root); err != nil {
						return err
					}
				}
			}
			if remove {
				return a.print(removeResult{Removed: changed, Unchanged: unchanged}, func(w io.Writer) {
					writePaths(w, "tess removed", changed)
					writePaths(w, "nothing to remove", unchanged)
				})
			}
			return a.print(setupResult{Written: changed, Unchanged: unchanged}, func(w io.Writer) {
				writePaths(w, "written", changed)
				writePaths(w, "unchanged", unchanged)
			})
		}),
	}
	cmd.Flags().BoolVar(&check, "check", false, "write nothing; exit status 1 if anything is missing or out of date")
	cmd.Flags().BoolVar(&remove, "remove", false, "take out what tess added, and leave the rest")
	return cmd
}

func writePaths(w io.Writer, what string, paths []string) {
	for _, path := range paths {
		fmt.Fprintf(w, "%s: %s\n", path, what)
	}
}
