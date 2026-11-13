// Package cli is the command-line adapter over the tracker. Main runs the
// program using os.Args, os.Stdin, os.Stdout, os.Stderr and the environment,
// and returns the process exit code (acceptance/README.md, "Entry point").
package cli

import (
	"fmt"
	"os"
)

// Main is the single entry point; cmd/tess/main.go is os.Exit(cli.Main()).
func Main() int {
	fmt.Fprintln(os.Stderr, "error: tess is not implemented yet")
	return 1
}
