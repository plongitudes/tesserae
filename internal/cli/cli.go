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
	"strings"

	"github.com/spf13/cobra"

	"github.com/plongitudes/tesserae/internal/tracker"
)

// Main is the single entry point; cmd/tess/main.go is os.Exit(cli.Main()).
func Main() int {
	return run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
}

// isTerminal reports whether a stream is a terminal, by the file's mode bits
// (ADR-0020: no terminal library).
func isTerminal(stream any) bool {
	f, ok := stream.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// app is one run of tess: its streams and its global flags.
type app struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	json   bool
	tty    bool // stdout is a terminal: lists are trees, and there is colour

	stdinRead bool // a text flag has taken stdin
}

// run runs one command line and returns the exit code: 0 on success, 1 on
// any error. The working directory and the environment are the process's.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	a := &app{stdin: stdin, stdout: stdout, stderr: stderr, tty: isTerminal(stdout)}
	return a.run(args)
}

func (a *app) run(args []string) int {
	root := a.rootCommand()
	root.SetArgs(args)
	// The command comes back even on an error: the one that was run, or the
	// one whose command line couldn't be read.
	cmd, err := root.ExecuteContextC(context.Background())
	if err != nil {
		// A command line that failed to parse may not have reached --json.
		a.printError(err, cmd, a.json || askedForJSON(args))
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

// usageError is a command-line error that tess itself finds: arguments or
// flags that cobra let through but that don't make a command (cli.md,
// Output). It is printed as cobra's own command-line errors are, with the
// command's use line where a refusal has its hint.
type usageError struct {
	message string
	code    string // "" is invalid_argument
}

func (u *usageError) Error() string { return u.message }

// usage makes a command-line error. The message has to carry whatever the
// use line can't say, such as the values a flag takes.
func usage(format string, args ...any) error {
	return &usageError{message: fmt.Sprintf(format, args...)}
}

// action wraps a command's body so that every error leaving it is a refusal
// with a code, a command-line error, or a failure.
func action(fn func(cmd *cobra.Command, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		err := fn(cmd, args)
		var refusal *tracker.Error
		var commandLine *usageError
		if err == nil || errors.As(err, &refusal) || errors.As(err, &commandLine) {
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
// an `error:` line followed by one of two things (cli.md, Output). A
// refusal has its hint, the next command to try. A command-line error has
// the synopsis of cmd, the command it was made for (a second form on an
// "or:" line, as git prints one), and where that command's examples are; in
// the envelope the synopsis is the hint.
func (a *app) printError(err error, cmd *cobra.Command, asJSON bool) {
	refusal, commandLine := classify(err)
	var lines []string
	if commandLine {
		lines = synopsis(cmd)
		refusal.Hint = strings.Join(lines, "\n")
	}
	switch {
	case asJSON:
		body := errorBody{Code: refusal.Code, Message: refusal.Message}
		if refusal.Hint != "" {
			body.Hint = &refusal.Hint
		}
		writeJSON(a.stderr, envelope{body})
	case commandLine:
		fmt.Fprintf(a.stderr, "error: %s\nusage: %s\nrun '%s --help' for examples\n",
			refusal.Message, strings.Join(lines, "\n   or: "), cmd.CommandPath())
	default:
		fmt.Fprintf(a.stderr, "error: %s\n", refusal.Message)
		if refusal.Hint != "" {
			fmt.Fprintf(a.stderr, "hint: %s\n", refusal.Hint)
		}
	}
}

// classify gives every error a code, and says whether it is a command-line
// error. Refusals carry their own code; a failure is internal. Anything
// else is a command-line error, which is invalid_argument: a usageError, or
// an error cobra made while reading the command line (an unknown command or
// flag, a bad flag value, the wrong number of arguments).
func classify(err error) (refusal *tracker.Error, commandLine bool) {
	if errors.As(err, &refusal) {
		// A copy: the caller may fill in the hint.
		return &tracker.Error{Code: refusal.Code, Message: refusal.Message, Hint: refusal.Hint}, false
	}
	var f *failure
	if errors.As(err, &f) {
		return &tracker.Error{Code: codeInternal, Message: err.Error()}, false
	}
	var u *usageError
	if errors.As(err, &u) && u.code != "" {
		return &tracker.Error{Code: u.code, Message: err.Error()}, true
	}
	return &tracker.Error{Code: tracker.CodeInvalidArgument, Message: err.Error()}, true
}

// invalidArgument is a refusal the CLI makes itself, for something wrong
// that the command's use line wouldn't help with: the environment, a file.
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
