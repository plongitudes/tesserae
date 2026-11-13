// Package cli is the command-line adapter over the tracker. Main runs the
// program using os.Args, os.Stdin, os.Stdout, os.Stderr and the environment,
// and returns the process exit code (acceptance/README.md, "Entry point").
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/plongitudes/tesserae/internal/tracker"
)

// Main is the single entry point; cmd/tess/main.go is os.Exit(cli.Main()).
func Main() int {
	return run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
}

// app is one run of tess: its streams and its global flags.
type app struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	json   bool
}

// run runs one command line and returns the exit code: 0 on success, 1 on
// any error. The working directory and the environment are the process's.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	a := &app{stdin: stdin, stdout: stdout, stderr: stderr}
	root := a.rootCommand()
	root.SetArgs(args)
	if err := root.ExecuteContext(context.Background()); err != nil {
		// A command line that failed to parse may not have reached --json.
		a.printError(err, a.json || askedForJSON(args))
		return 1
	}
	return 0
}

// askedForJSON reports whether --json is among the flags of a command line.
func askedForJSON(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "--":
			return false
		case "--json", "--json=true":
			return true
		}
	}
	return false
}

// failure is an error from running a command that isn't a refusal: the
// database couldn't be read, git failed, and so on. It keeps such errors
// apart from the ones cobra makes while parsing the command line.
type failure struct{ err error }

func (f *failure) Error() string { return f.err.Error() }
func (f *failure) Unwrap() error { return f.err }

// CodeInternal is the code of an error that is no one's refusal.
const codeInternal = "internal"

// action wraps a command's body so that every error leaving it is either a
// refusal with a code or a failure.
func action(fn func(cmd *cobra.Command, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		err := fn(cmd, args)
		var refusal *tracker.Error
		if err == nil || errors.As(err, &refusal) {
			return err
		}
		return &failure{err}
	}
}

// envelope is the error object of acceptance/README.md, "Errors".
type envelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string  `json:"code"`
	Message string  `json:"message"`
	Hint    *string `json:"hint"`
}

// printError writes err to stderr: the envelope with --json, and otherwise
// an `error:` line, with the hint on a line of its own.
func (a *app) printError(err error, asJSON bool) {
	refusal := classify(err)
	if asJSON {
		body := errorBody{Code: refusal.Code, Message: refusal.Message}
		if refusal.Hint != "" {
			body.Hint = &refusal.Hint
		}
		writeJSON(a.stderr, envelope{body})
		return
	}
	fmt.Fprintf(a.stderr, "error: %s\n", refusal.Message)
	if refusal.Hint != "" {
		fmt.Fprintf(a.stderr, "hint: %s\n", refusal.Hint)
	}
}

// classify gives every error a code. Refusals carry their own; a failure is
// internal; anything else was made by cobra while reading the command line
// (an unknown command or flag, a bad flag value, the wrong number of
// arguments), which is invalid_argument.
func classify(err error) *tracker.Error {
	var refusal *tracker.Error
	if errors.As(err, &refusal) {
		return refusal
	}
	var f *failure
	if errors.As(err, &f) {
		return &tracker.Error{Code: codeInternal, Message: err.Error()}
	}
	return invalidArgument(err.Error(), "run `tess --help` for the commands, or `tess COMMAND --help` for one command's flags")
}

func invalidArgument(message, hint string) *tracker.Error {
	return &tracker.Error{Code: tracker.CodeInvalidArgument, Message: message, Hint: hint}
}

// writeJSON writes v as one line of JSON.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// print writes a command's result: v as JSON with --json, and otherwise
// whatever plain writes.
func (a *app) print(v any, plain func(w io.Writer)) error {
	if a.json {
		return writeJSON(a.stdout, v)
	}
	plain(a.stdout)
	return nil
}
