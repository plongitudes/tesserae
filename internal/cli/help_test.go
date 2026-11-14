package cli

import (
	"io"
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
