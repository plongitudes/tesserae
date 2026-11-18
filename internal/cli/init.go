package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/plongitudes/tesserae/internal/tracker"
)

// initResult is init's JSON (acceptance/README.md, "Command results").
type initResult struct {
	Prefix   string `json:"prefix"`
	Database string `json:"database"`
}

func (a *app) initCommand() *cobra.Command {
	var prefix string
	cmd := &cobra.Command{
		Use:   "init [--prefix PREFIX]",
		Short: "Create the database for this repository",
		Long: `Create the database for this repository.

The database is a single file in git's common directory, so every worktree of
the repository shares it and nothing is added to the working tree. The prefix
starts every new issue's ID; it defaults to the repository's directory name.`,
		Example: `  tess init
  tess init --prefix api
  tess init --prefix api --json`,
		Args: cobra.NoArgs,
		RunE: action(func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			path, err := databasePath(ctx)
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("prefix") {
				if prefix, err = defaultPrefix(path); err != nil {
					return err
				}
			}
			if err := tracker.Init(ctx, path, prefix); err != nil {
				return err
			}
			return a.print(initResult{Prefix: prefix, Database: path}, func(w io.Writer) {
				fmt.Fprintf(w, "Created %s (prefix %s)\n", path, prefix)
			})
		}),
	}
	cmd.Flags().StringVar(&prefix, "prefix", "", "the `PREFIX` new issue IDs start with: a lowercase letter, then letters and digits (default: the repository's directory name)")
	return cmd
}

// defaultPrefix makes a prefix from the name of the repository's directory,
// the one holding the database's .git: lowercased, with everything but
// letters and digits dropped, and starting at its first letter.
func defaultPrefix(database string) (string, error) {
	commonDir := filepath.Dir(database)
	name := filepath.Base(commonDir)
	if name == ".git" {
		name = filepath.Base(filepath.Dir(commonDir))
	} else {
		name = strings.TrimSuffix(name, ".git") // a bare repository, proj.git
	}
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9' && b.Len() > 0:
			b.WriteRune(r)
		}
	}
	if !tracker.ValidPrefix(b.String()) {
		return "", invalidArgument(
			fmt.Sprintf("can't make an ID prefix from the directory name %q", name),
			"choose one with --prefix, e.g. `tess init --prefix proj`")
	}
	return b.String(), nil
}
