package cli

import (
	"strings"

	"github.com/spf13/cobra"
)

// Help reads as man-page syntax (cli.md, Output). A command's Use is its
// full synopsis in POSIX notation: [ ] optional, ... repeatable, |
// alternatives, each flag that takes a value with its CAPS placeholder. A
// command with two forms has two lines in Use, each starting with its name.
// The flags table takes the same placeholders from the backticked word in
// each flag's usage string.

// synopsis is cmd's synopsis lines, each with the whole command path. It is
// what help prints under "Usage:" and what a command-line error prints as
// its usage; cobra's own UseLine knows nothing of a second line.
func synopsis(cmd *cobra.Command) []string {
	lines := strings.Split(cmd.Use, "\n")
	if cmd.HasParent() {
		for i := range lines {
			lines[i] = cmd.Parent().CommandPath() + " " + lines[i]
		}
	}
	return lines
}

func init() {
	cobra.AddTemplateFunc("synopsis", synopsis)
}

// usageTemplate is cobra's own, with two changes: "Usage:" lists the
// synopsis lines and nothing else (no "[flags]", no "[command]"), and the
// last line names the placeholder the way the synopsis does.
const usageTemplate = `Usage:{{range synopsis .}}
  {{.}}{{end}}{{if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Examples:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}

Available Commands:{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Global Flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableSubCommands}}

Run '{{.CommandPath}} COMMAND --help' for a command's flags and examples.{{end}}
`

// asSyntax makes root and every command under it print help as syntax.
// The template is inherited from root; the flag is not.
func asSyntax(root *cobra.Command) {
	root.SetUsageTemplate(usageTemplate)
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		cmd.DisableFlagsInUseLine = true
		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}
	walk(root)
}
