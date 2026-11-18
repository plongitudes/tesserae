package cli

import (
	"io"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/plongitudes/tesserae/internal/tracker"
)

// everyCommand is the root command and every command under it.
func everyCommand(t *testing.T) []*cobra.Command {
	t.Helper()
	a := &app{stdin: strings.NewReader(""), stdout: io.Discard, stderr: io.Discard}
	var all []*cobra.Command
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		all = append(all, cmd)
		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}
	walk(a.rootCommand())
	return all
}

// shellWords splits an example line the way a shell would, far enough for
// help text: quotes group, a `#` word starts a comment, `|` stands alone.
func shellWords(t *testing.T, line string) []string {
	t.Helper()
	var (
		words   []string
		word    strings.Builder
		quote   rune
		started bool
	)
	flush := func() {
		if started {
			words = append(words, word.String())
			word.Reset()
			started = false
		}
	}
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, started = r, true
		case r == ' ':
			flush()
		case r == '#' && !started:
			return words
		default:
			word.WriteRune(r)
			started = true
		}
	}
	if quote != 0 {
		t.Fatalf("unclosed quote in example %q", line)
	}
	flush()
	return words
}

// tessInvocation is the `tess ...` stage of an example's pipeline, without
// the leading `tess`.
func tessInvocation(t *testing.T, line string) []string {
	t.Helper()
	var stage []string
	for _, word := range append(shellWords(t, line), "|") {
		if word != "|" {
			stage = append(stage, word)
			continue
		}
		if len(stage) > 0 && stage[0] == "tess" {
			return stage[1:]
		}
		stage = nil
	}
	t.Fatalf("example %q does not run tess", line)
	return nil
}

func TestEveryCommandsHelpHasAnExamplesSection(t *testing.T) {
	isolate(t)
	for _, cmd := range everyCommand(t) {
		args := append(strings.Fields(cmd.CommandPath())[1:], "--help")
		t.Run(cmd.CommandPath(), func(t *testing.T) {
			r := tess(t, args...)
			if r.code != 0 || r.stderr != "" {
				t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
			}
			if !strings.Contains(r.stdout, "\nExamples:\n") {
				t.Errorf("no Examples section in:\n%s", r.stdout)
			}
		})
	}
}

// An example must be something that can be typed: it names its own command,
// uses only flags the command has, and gives it arguments it accepts.
func TestEveryExampleIsARealInvocation(t *testing.T) {
	for _, cmd := range everyCommand(t) {
		t.Run(cmd.CommandPath(), func(t *testing.T) {
			lines := 0
			for _, line := range strings.Split(cmd.Example, "\n") {
				if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
					continue
				}
				lines++
				if !strings.HasPrefix(line, "  ") {
					t.Errorf("example %q is not indented by two spaces", line)
				}
				args := tessInvocation(t, line)
				found, rest, err := cmd.Root().Find(args)
				if err != nil {
					t.Errorf("example %q: %v", line, err)
					continue
				}
				// A command with commands under it shows those too.
				under := false
				for c := found; c != nil; c = c.Parent() {
					under = under || c == cmd
				}
				if !under {
					t.Errorf("example %q runs %q, not %q", line, found.CommandPath(), cmd.CommandPath())
					continue
				}
				if err := found.ParseFlags(rest); err != nil {
					t.Errorf("example %q: %v", line, err)
					continue
				}
				if err := found.ValidateArgs(found.Flags().Args()); err != nil {
					t.Errorf("example %q: %v", line, err)
				}
			}
			if lines == 0 {
				t.Error("no examples")
			}
		})
	}
}

func TestStatusHelpListsTheSettableValues(t *testing.T) {
	isolate(t)
	help := tess(t, "status", "--help").stdout
	for _, status := range tracker.Statuses {
		if !strings.Contains(help, "\n  "+status+" ") {
			t.Errorf("status --help has no line for %q:\n%s", status, help)
		}
	}
	if !strings.Contains(help, "waiting") {
		t.Errorf("status --help does not say that waiting can't be set:\n%s", help)
	}
}

func TestLinkHelpShowsEveryWordingAndItsInverse(t *testing.T) {
	isolate(t)
	for _, name := range []string{"link", "unlink"} {
		help := tess(t, name, "--help").stdout
		for _, wording := range tracker.LinkWordings {
			if !strings.Contains(help, " "+wording+" ") {
				t.Errorf("%s --help does not show %q:\n%s", name, wording, help)
			}
		}
	}
	help := tess(t, "link", "--help").stdout
	for _, example := range []string{"tess link api-7kq blocks api-m2x", "tess link api-m2x blocked-by api-7kq"} {
		if !strings.Contains(help, example) {
			t.Errorf("link --help has no example %q:\n%s", example, help)
		}
	}
}

func TestHelpNeedsNoRepository(t *testing.T) {
	isolate(t)
	r := tess(t, "create", "--help")
	if r.code != 0 || !strings.Contains(r.stdout, "Examples:") {
		t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
}

// everyWord is everything tess says about how to use it: the orientation,
// each command's help, and the skill.
func everyWord(t *testing.T) map[string]string {
	t.Helper()
	inDir(t, isolate(t), "anywhere")
	words := map[string]string{
		"the orientation": tess(t).stdout,
		"the skill":       string(skillText),
	}
	for _, cmd := range everyCommand(t) {
		words[cmd.CommandPath()+" --help"] = cmd.Short + "\n" + cmd.Long + "\n" + cmd.UsageString()
	}
	return words
}

// placeholders are the words a value may be called in a synopsis or a flags
// table (cli.md, Output), and PREFIX, for init's one flag.
var placeholders = []string{
	"TITLE", "ID", "TYPE", "N", "LABEL", "NAME", "TEXT", "DATE", "REASON",
	"STATUS", "FIELD", "PATH", "PREFIX",
}

// flagLine is one line of a flags table: the short name, the long name, and
// the word that stands for the value, if the flag takes one.
var flagLine = regexp.MustCompile(`^\s+(?:-(\w), )?--([\w-]+)(?: (\S+))?(?:\s{3,}\S|$)`)

// Help reads as man-page syntax (cli.md, Output): the usage is a full
// synopsis, never "[flags]"; a flag that takes a value names it with a CAPS
// placeholder, in the flags table and in the synopsis, never with a Go type.
func TestHelpReadsAsSyntax(t *testing.T) {
	for _, cmd := range everyCommand(t) {
		t.Run(cmd.CommandPath(), func(t *testing.T) {
			help := cmd.UsageString()
			for _, never := range []string{"[flags]", "[command]"} {
				if strings.Contains(help, never) {
					t.Errorf("the help says %s:\n%s", never, help)
				}
			}
			lines := synopsis(cmd)
			_, usage, _ := strings.Cut(help, "Usage:\n")
			for _, line := range lines {
				if !strings.HasPrefix(line, cmd.CommandPath()) {
					t.Errorf("synopsis line %q doesn't start with %q", line, cmd.CommandPath())
				}
				if !strings.HasPrefix(usage, "  "+line+"\n") {
					t.Errorf("the help doesn't show the synopsis line %q under Usage:\n%s", line, help)
				}
				usage = strings.TrimPrefix(usage, "  "+line+"\n")
			}
			if cmd.UseLine() != lines[0] && len(lines) == 1 {
				t.Errorf("cobra's use line is %q, the synopsis %q", cmd.UseLine(), lines[0])
			}
			all := strings.Join(lines, "\n")

			local := cmd.LocalFlags().FlagUsages()
			table := local + cmd.InheritedFlags().FlagUsages()
			seen := 0
			for _, line := range strings.Split(strings.TrimRight(table, "\n"), "\n") {
				m := flagLine.FindStringSubmatch(line)
				if m == nil {
					continue // the rest of a wrapped description
				}
				seen++
				short, name, value := m[1], m[2], m[3]
				flag := cmd.Flags().Lookup(name)
				if flag == nil {
					t.Fatalf("the flags table names --%s, which isn't a flag:\n%s", name, table)
				}
				takesValue := flag.Value.Type() != "bool"
				switch {
				case !takesValue && value != "":
					t.Errorf("--%s takes no value, but the table shows %q", name, value)
				case takesValue && !slices.Contains(placeholders, value):
					t.Errorf("--%s is shown with %q, which isn't a placeholder", name, value)
				}
				if name == "help" || name == "json" || !strings.Contains(local, line) {
					continue // on every command: in the table, not the synopsis
				}
				forms := []string{"--" + name}
				if short != "" {
					forms = append(forms, "-"+short)
				}
				if takesValue {
					for i := range forms {
						forms[i] += " " + value
					}
				}
				if !slices.ContainsFunc(forms, func(form string) bool {
					return regexp.MustCompile(`(?m)[\[ |]` + regexp.QuoteMeta(form) + `([\] |]|$)`).MatchString(all)
				}) {
					t.Errorf("the synopsis doesn't show %s:\n%s", forms[0], all)
				}
			}
			if seen == 0 {
				t.Errorf("no flag line was read from the table:\n%s", table)
			}
		})
	}
}

// A command with two forms has two synopsis lines, in help and in the
// usage of a command-line error (cli.md, Output).
func TestShowHasTwoSynopsisLines(t *testing.T) {
	inInitialisedRepo(t)
	r := tess(t, "show", "--help")
	want := "Usage:\n  tess show ID... [--history] [--tree]\n  tess show ID --field FIELD\n"
	if !strings.Contains(r.stdout, want) {
		t.Errorf("show --help doesn't have\n%s\nin\n%s", want, r.stdout)
	}
	r = tess(t, "show")
	want = "usage: tess show ID... [--history] [--tree]\n   or: tess show ID --field FIELD\nrun 'tess show --help' for examples\n"
	if !strings.HasSuffix(r.stderr, want) {
		t.Errorf("stderr is %q, want it to end with %q", r.stderr, want)
	}
}

// The forms the dogfood pass added are shown where a reader looks for them:
// in the help of the command that has them, and in the skill.
func TestTheWordsShowTheNewForms(t *testing.T) {
	words := everyWord(t)
	for where, wants := range map[string][]string{
		"tess link --help":   {"tess link api-7kq api-m2x blocks api-r4d", "ancestor"},
		"tess unlink --help": {"tess unlink api-7kq api-m2x blocks api-r4d"},
		"tess show --help":   {"tess show api-7kq --tree", "tess show api-7kq --field notes"},
		"tess update --help": {"--notes \"Retried with a longer timeout; same failure\" --append"},
		"tess list --help":   {"tess list -s 7kq", "only issues whose id, title"},
		"the orientation":    {"--tree", "--field", "--append", "-s", "tess link A blocks B"},
		"the skill": {
			"tess link A B blocks C", "tess show ID --tree", "tess show ID --field notes",
			"--notes - --append", "`-s TEXT` (matches the ID",
		},
	} {
		text, ok := words[where]
		if !ok {
			t.Errorf("there is no %s", where)
		}
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Errorf("%s doesn't show %q", where, want)
			}
		}
	}
}
