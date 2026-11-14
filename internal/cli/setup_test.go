package cli

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

const (
	skillFile    = ".claude/skills/tess/SKILL.md"
	settingsFile = ".claude/settings.json"
)

// someoneElses is a settings file with content tess must leave alone: keys in
// an order that isn't alphabetical, a hook of the user's own, and a command
// with characters JSON encoders like to escape.
const someoneElses = `{
  "model": "opus",
  "env": { "KEEP_ME": "yes", "A_NUMBER": 1.50 },
  "permissions": { "deny": ["Read(.env)"], "allow": ["Bash(npm test)"] },
  "hooks": {
    "SessionStart": [
      { "matcher": "startup", "hooks": [{ "type": "command", "command": "echo hi >&2 && true" }] }
    ]
  }
}
`

func fileText(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func exists(name string) bool {
	_, err := os.Stat(name)
	return err == nil
}

func settings(t *testing.T) object {
	t.Helper()
	return decode[object](t, fileText(t, settingsFile))
}

// hookCommands lists the commands of every hook in an event's groups.
func hookCommands(t *testing.T, s object, event string) []string {
	t.Helper()
	var commands []string
	hooks, _ := s["hooks"].(object)
	groups, _ := hooks[event].([]any)
	for _, group := range groups {
		for _, hook := range group.(object)["hooks"].([]any) {
			commands = append(commands, hook.(object)["command"].(string))
		}
	}
	return commands
}

func setup(t *testing.T, args ...string) object {
	t.Helper()
	return ok[object](t, append([]string{"setup", "claude"}, args...)...)
}

func TestSetupClaudeWritesTheSkillAndTheSettings(t *testing.T) {
	inRepo(t, "repo")

	got := setup(t)
	want := object{"written": []any{skillFile, settingsFile}, "unchanged": []any{}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("setup claude printed %v, want %v", got, want)
	}

	skill := fileText(t, skillFile)
	head, body, found := strings.Cut(strings.TrimPrefix(skill, "---\n"), "\n---\n")
	if !strings.HasPrefix(skill, "---\n") || !found {
		t.Fatalf("the skill has no frontmatter:\n%s", skill)
	}
	front := map[string]string{}
	for _, line := range strings.Split(head, "\n") {
		key, value, _ := strings.Cut(line, ": ")
		front[key] = value
	}
	if front["name"] != "tess" || front["allowed-tools"] != "Bash(tess *)" || len(front) != 3 {
		t.Errorf("frontmatter is %v", front)
	}
	if n := len(front["description"]); n < 200 || n > 400 {
		t.Errorf("the description is %d characters, want about 300: %s", n, front["description"])
	}
	if n := strings.Count(body, "\n"); n < 40 || n > 60 {
		t.Errorf("the skill's body is %d lines, want 40 to 60", n)
	}
	for _, want := range []string{"tess ready", "tess status", "in-progress", "tess create", "--json", "newly_unblocked", "when you finish a unit of work"} {
		if !strings.Contains(body, want) {
			t.Errorf("the skill's body doesn't mention %q", want)
		}
	}
	if !strings.Contains(body, "`tess sync` when you finish a unit of work") {
		t.Errorf("the skill doesn't say to sync when a unit of work is finished")
	}

	s := settings(t)
	if allow := s["permissions"].(object)["allow"]; !reflect.DeepEqual(allow, []any{"Bash(tess *)"}) {
		t.Errorf("permissions.allow is %v", allow)
	}
	wantStart := []any{object{
		"matcher": "startup|resume|compact",
		"hooks":   []any{object{"type": "command", "command": "command -v tess >/dev/null 2>&1 && tess hook session-start || true"}},
	}}
	wantEnd := []any{object{
		"hooks": []any{object{"type": "command", "command": "command -v tess >/dev/null 2>&1 && tess hook session-end || true"}},
	}}
	hooks := s["hooks"].(object)
	if !reflect.DeepEqual(hooks["SessionStart"], wantStart) {
		t.Errorf("SessionStart is %v", hooks["SessionStart"])
	}
	if !reflect.DeepEqual(hooks["SessionEnd"], wantEnd) {
		t.Errorf("SessionEnd is %v", hooks["SessionEnd"])
	}
	if raw := fileText(t, settingsFile); !strings.Contains(raw, ">/dev/null 2>&1 &&") || !strings.HasSuffix(raw, "}\n") {
		t.Errorf("settings.json is not written plainly:\n%s", raw)
	}
}

func TestSetupClaudeNeedsARepositoryButNoDatabase(t *testing.T) {
	inDir(t, isolate(t), "nowhere")
	wantError(t, tess(t, "setup", "claude", "--json"), "not_a_git_repo")
	if exists(".claude") {
		t.Error("setup wrote outside a repository")
	}
}

func TestSetupClaudeWritesAtTheTopOfTheWorktree(t *testing.T) {
	inRepo(t, "repo")
	if err := os.Mkdir("sub", 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir("sub")
	setup(t)
	if !exists("../"+skillFile) || !exists("../"+settingsFile) || exists(".claude") {
		t.Error("setup from a subdirectory didn't write at the top of the worktree")
	}
}

func TestSetupClaudeKeepsWhatIsAlreadyInTheSettings(t *testing.T) {
	inRepo(t, "repo")
	if err := os.Mkdir(".claude", 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, settingsFile, someoneElses)

	setup(t)
	s := settings(t)
	if s["model"] != "opus" || !reflect.DeepEqual(s["env"], object{"KEEP_ME": "yes", "A_NUMBER": 1.5}) {
		t.Errorf("other keys were disturbed: %v", s)
	}
	permissions := s["permissions"].(object)
	if !reflect.DeepEqual(permissions["allow"], []any{"Bash(npm test)", "Bash(tess *)"}) || !reflect.DeepEqual(permissions["deny"], []any{"Read(.env)"}) {
		t.Errorf("permissions are %v", permissions)
	}
	if got := hookCommands(t, s, "SessionStart"); len(got) != 2 || got[0] != "echo hi >&2 && true" || !strings.Contains(got[1], "tess hook session-start") {
		t.Errorf("SessionStart commands are %q", got)
	}

	// Keys stay in the order the file had them, new ones go last, and
	// numbers and commands are kept as written.
	raw := fileText(t, settingsFile)
	last := -1
	for _, key := range []string{`"model"`, `"env"`, `"KEEP_ME"`, `"A_NUMBER": 1.50`, `"permissions"`, `"deny"`, `"allow"`, `"hooks"`, `"SessionStart"`, `echo hi >&2 && true`, `"SessionEnd"`} {
		at := strings.Index(raw, key)
		if at <= last {
			t.Errorf("%s is out of place or missing in:\n%s", key, raw)
		}
		last = at
	}
}

func TestSetupClaudeAgainWritesNothing(t *testing.T) {
	inRepo(t, "repo")
	if err := os.Mkdir(".claude", 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, settingsFile, someoneElses)
	setup(t)
	skill, settingsBefore := fileText(t, skillFile), fileText(t, settingsFile)

	// A current file is left byte for byte, even when it isn't laid out the
	// way tess would write it.
	compact, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatal(err)
	}
	relaid := strings.ReplaceAll(string(compact), "\n  ", "\n\t")
	if relaid == settingsBefore {
		t.Fatal("the test's relaid settings are the same as the written ones")
	}
	writeFile(t, settingsFile, relaid)

	got := setup(t)
	want := object{"written": []any{}, "unchanged": []any{skillFile, settingsFile}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the second run printed %v, want %v", got, want)
	}
	if fileText(t, skillFile) != skill || fileText(t, settingsFile) != relaid {
		t.Error("the second run changed a file")
	}
}

func TestSetupClaudeCheckFailsUntilEverythingIsCurrent(t *testing.T) {
	inRepo(t, "repo")

	e := wantError(t, tess(t, "setup", "claude", "--check", "--json"), "setup_needed")
	if msg := e["message"].(string); !strings.Contains(msg, skillFile+" is missing") || !strings.Contains(msg, settingsFile+" is missing") {
		t.Errorf("message is %q", msg)
	}
	if hint, _ := e["hint"].(string); !strings.Contains(hint, "tess setup claude") {
		t.Errorf("hint is %q", hint)
	}
	if exists(".claude") {
		t.Error("--check wrote something")
	}

	setup(t)
	got := setup(t, "--check")
	want := object{"written": []any{}, "unchanged": []any{skillFile, settingsFile}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("--check printed %v, want %v", got, want)
	}

	// An edited skill is out of date; --check says so and leaves it.
	writeFile(t, skillFile, "an older skill\n")
	e = wantError(t, tess(t, "setup", "claude", "--check", "--json"), "setup_needed")
	if msg := e["message"]; msg != skillFile+" is out of date" {
		t.Errorf("message is %q", msg)
	}
	if fileText(t, skillFile) != "an older skill\n" {
		t.Error("--check changed the skill")
	}
	r := tess(t, "setup", "claude", "--check")
	if r.code != 1 || r.stdout != "" || !strings.HasPrefix(r.stderr, "error: "+skillFile+" is out of date\n") {
		t.Errorf("plain --check: exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}

	got = setup(t)
	want = object{"written": []any{skillFile}, "unchanged": []any{settingsFile}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("setup after an edit printed %v, want %v", got, want)
	}
}

func TestSetupClaudeReplacesAnOutOfDateOrDoubledHook(t *testing.T) {
	inRepo(t, "repo")
	if err := os.Mkdir(".claude", 0o755); err != nil {
		t.Fatal(err)
	}
	// An older tess wrote the start hook bare and without a matcher, inside
	// a group that also holds someone else's hook; the end hook is there twice.
	writeFile(t, settingsFile, `{
  "permissions": {"allow": ["Bash(tess *)"]},
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "tess hook session-start"}, {"type": "command", "command": "echo mine"}]}
    ],
    "SessionEnd": [
      {"hooks": [{"command": "command -v tess >/dev/null 2>&1 && tess hook session-end || true", "type": "command"}]},
      {"hooks": [{"type": "command", "command": "command -v tess >/dev/null 2>&1 && tess hook session-end || true"}]}
    ]
  }
}
`)
	wantError(t, tess(t, "setup", "claude", "--check", "--json"), "setup_needed")
	setup(t)

	s := settings(t)
	wantStart := []string{"echo mine", "command -v tess >/dev/null 2>&1 && tess hook session-start || true"}
	if got := hookCommands(t, s, "SessionStart"); !reflect.DeepEqual(got, wantStart) {
		t.Errorf("SessionStart commands are %q, want %q", got, wantStart)
	}
	wantEnd := []string{"command -v tess >/dev/null 2>&1 && tess hook session-end || true"}
	if got := hookCommands(t, s, "SessionEnd"); !reflect.DeepEqual(got, wantEnd) {
		t.Errorf("SessionEnd commands are %q, want %q", got, wantEnd)
	}
	if allow := s["permissions"].(object)["allow"]; !reflect.DeepEqual(allow, []any{"Bash(tess *)"}) {
		t.Errorf("permissions.allow is %v", allow)
	}
	setup(t, "--check")
}

func TestSetupClaudeTakesAHookWithItsKeysInAnotherOrderAsCurrent(t *testing.T) {
	inRepo(t, "repo")
	setup(t)
	reordered := strings.Replace(fileText(t, settingsFile),
		`"type": "command",
            "command": "command -v tess >/dev/null 2>&1 && tess hook session-end || true"`,
		`"command": "command -v tess >/dev/null 2>&1 && tess hook session-end || true",
            "type": "command"`, 1)
	if reordered == fileText(t, settingsFile) {
		t.Fatalf("the test didn't reorder anything in:\n%s", reordered)
	}
	writeFile(t, settingsFile, reordered)
	setup(t, "--check")
}

func TestSetupClaudeRemoveTakesOutOnlyWhatTessAdded(t *testing.T) {
	inRepo(t, "repo")
	if err := os.Mkdir(".claude", 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, settingsFile, someoneElses)
	before := settings(t)
	setup(t)

	got := setup(t, "--remove")
	want := object{"removed": []any{skillFile, settingsFile}, "unchanged": []any{}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("--remove printed %v, want %v", got, want)
	}
	if exists(".claude/skills") {
		t.Error("the skill, or its empty directories, are still there")
	}
	if after := settings(t); !reflect.DeepEqual(after, before) {
		t.Errorf("settings after --remove are %v, want what was there before setup: %v", after, before)
	}

	got = setup(t, "--remove")
	want = object{"removed": []any{}, "unchanged": []any{skillFile, settingsFile}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("a second --remove printed %v, want %v", got, want)
	}
}

func TestSetupClaudeRemoveLeavesNothingBehindInAFreshRepository(t *testing.T) {
	inRepo(t, "repo")
	setup(t)
	setup(t, "--remove")
	if exists(".claude") {
		t.Error(".claude is still there, though tess made all of it")
	}
}

func TestSetupClaudeRemoveKeepsOtherSkillsAndASharedHookGroup(t *testing.T) {
	inRepo(t, "repo")
	setup(t)
	if err := os.MkdirAll(".claude/skills/other", 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, ".claude/skills/other/SKILL.md", "theirs\n")
	withTheirs := strings.Replace(fileText(t, settingsFile),
		`"hooks": [
          {`, `"hooks": [
          {"type": "command", "command": "echo theirs"},
          {`, 1)
	writeFile(t, settingsFile, withTheirs)

	setup(t, "--remove")
	if !exists(".claude/skills/other/SKILL.md") {
		t.Error("--remove took someone else's skill")
	}
	s := settings(t)
	if got := hookCommands(t, s, "SessionStart"); !reflect.DeepEqual(got, []string{"echo theirs"}) {
		t.Errorf("SessionStart commands are %q, want only theirs", got)
	}
	if _, ok := s["permissions"]; ok || len(s["hooks"].(object)) != 1 {
		t.Errorf("settings are %v", s)
	}
}

func TestSetupClaudeRefusesSettingsItCannotRead(t *testing.T) {
	for name, text := range map[string]string{
		"not JSON":             `{"permissions": `,
		"not an object":        `["Bash(tess *)"]`,
		"permissions a string": `{"permissions": "all"}`,
		"allow an object":      `{"permissions": {"allow": {}}}`,
		"hooks an array":       `{"hooks": []}`,
		"an event an object":   `{"hooks": {"SessionEnd": {}}}`,
		"text after the value": `{} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			inRepo(t, "repo")
			if err := os.Mkdir(".claude", 0o755); err != nil {
				t.Fatal(err)
			}
			writeFile(t, settingsFile, text)
			for _, flag := range []string{"--json", "--check", "--remove"} {
				e := wantError(t, tess(t, "setup", "claude", "--json", flag), "invalid_argument")
				if msg := e["message"].(string); !strings.Contains(msg, settingsFile) {
					t.Errorf("%s: message is %q", flag, msg)
				}
			}
			if fileText(t, settingsFile) != text || exists(skillFile) {
				t.Error("a refused setup changed a file")
			}
		})
	}
}

func TestSetupTakesOnlyClaudeAndOneModeAtATime(t *testing.T) {
	inRepo(t, "repo")
	wantError(t, tess(t, "setup", "--json"), "invalid_argument")
	wantError(t, tess(t, "setup", "cursor", "--json"), "invalid_argument")
	wantError(t, tess(t, "setup", "claude", "extra", "--json"), "invalid_argument")
	wantError(t, tess(t, "setup", "claude", "--check", "--remove", "--json"), "invalid_argument")
	if exists(".claude") {
		t.Error("a refused setup wrote something")
	}
}

func TestSetupClaudePlainOutputNamesEachFile(t *testing.T) {
	inRepo(t, "repo")
	wantPlain(t, tess(t, "setup", "claude"), skillFile+": written", settingsFile+": written")
	wantPlain(t, tess(t, "setup", "claude"), skillFile+": unchanged", settingsFile+": unchanged")
	wantPlain(t, tess(t, "setup", "claude", "--check"), skillFile+": current", settingsFile+": current")
	wantPlain(t, tess(t, "setup", "claude", "--remove"), skillFile+": tess removed", settingsFile+": tess removed")
	wantPlain(t, tess(t, "setup", "claude", "--remove"), skillFile+": nothing to remove", settingsFile+": nothing to remove")
}
