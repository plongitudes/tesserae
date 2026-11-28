package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/plongitudes/tesserae/internal/tracker"
)

// importResult is import's JSON (acceptance/README.md, "Command results").
type importResult struct {
	tracker.ImportResult
	Initialized bool `json:"initialized"`
}

func (a *app) importCommand() *cobra.Command {
	var prefix string
	cmd := &cobra.Command{
		Use:   "import PATH [--prefix PREFIX]",
		Short: "Bring in the issues of an old bd issues.jsonl, keeping their IDs",
		Long: `Bring in the issues of an old bd issues.jsonl, keeping their IDs.

PATH is the file, or a directory holding it: a repository with a .beads
directory, or that directory itself. Statuses and link types are mapped to the ones tess has; comments, labels and
timestamps are carried over. Either the whole file is imported or none of it.
An issue whose ID is already here is skipped, so importing a file again adds
only what is new.

A repository with no database yet gets one first, so moving over from bd is
one command. Its prefix is --prefix, or else the one prefix every imported
ID starts with, lowercased; the imported IDs keep their case.`,
		Example: `  tess import .                 # this repository's .beads/issues.jsonl
  tess import .beads/issues.jsonl --prefix proj
  tess import ../old-checkout --json`,
		Args: cobra.ExactArgs(1),
		RunE: action(func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			path, err := databasePath(ctx)
			if err != nil {
				return err
			}
			file, err := importFile(args[0])
			if err != nil {
				return err
			}
			// The file is read once and kept: it decides the prefix before
			// there is a database to import it into.
			data, err := os.ReadFile(file)
			if err != nil {
				return invalidArgument(
					fmt.Sprintf("can't read %s: %v", file, errors.Unwrap(err)),
					"import takes bd's issues.jsonl, or a directory holding .beads/issues.jsonl")
			}
			initialised, err := initialiseForImport(cmd, path, data, &prefix)
			if err != nil {
				return err
			}
			return withTracker(ctx, func(tr tracker.Tracker) error {
				result, err := tr.Import(ctx, bytes.NewReader(data))
				if err != nil {
					return err
				}
				return a.print(importResult{ImportResult: result, Initialized: initialised}, func(w io.Writer) {
					if initialised {
						fmt.Fprintln(w, initialisedLine(path, prefix))
					}
					fmt.Fprintf(w, "imported %d, skipped %d\n", result.Imported, result.Skipped)
				})
			})
		}),
	}
	cmd.Flags().StringVar(&prefix, "prefix", "", "the `PREFIX` new issue IDs start with, when the import creates the database (default: the prefix the imported IDs share)")
	return cmd
}

// importFile is the file an import reads for PATH: PATH itself, or for a
// directory the first of PATH/.beads/issues.jsonl and PATH/issues.jsonl
// that is there (cli.md, import). A directory with neither is refused,
// naming both.
func importFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return path, nil // a file, or nothing: the read says what is wrong
	}
	candidates := []string{filepath.Join(path, ".beads", "issues.jsonl"), filepath.Join(path, "issues.jsonl")}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c, nil
		}
	}
	return "", invalidArgument(
		fmt.Sprintf("%s is a directory with no bd issues: neither %s nor %s is there", path, candidates[0], candidates[1]),
		"give the path of bd's issues.jsonl, or of a repository that has a .beads directory")
}

// initialiseForImport creates the database when the repository has none
// (cli.md, import), with --prefix or the prefix the file's IDs share, and
// reports whether it did. --prefix is for creating one: on a repository
// that has a database it is refused. A file that is present but can't be
// opened is left to the open.
func initialiseForImport(cmd *cobra.Command, path string, data []byte, prefix *string) (bool, error) {
	chosen := cmd.Flags().Changed("prefix")
	if _, err := os.Stat(path); err == nil {
		if chosen {
			return false, invalidArgument(
				"this repository already has a tess database; --prefix is for creating one",
				"run `tess import PATH` without --prefix; `tess doctor` shows the prefix it has")
		}
		return false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if !chosen {
		p, err := tracker.ImportPrefix(bytes.NewReader(data))
		if err != nil {
			return false, err
		}
		*prefix = p
	}
	if err := tracker.Init(cmd.Context(), path, *prefix); err != nil {
		return false, err
	}
	return true, nil
}
