// Package git runs the user's git binary for the plumbing sync needs
// (docs/design/sync.md, "Git"; ADR-0020). Authentication, transports and
// credential helpers are whatever the user's git already has. Each
// operation is one git process, however many objects it touches. The
// package knows no ref names: callers pass them.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/plongitudes/tesserae/internal/tracker"
)

// Stable error codes (acceptance/README.md).
const (
	// CodeNotAGitRepo is for running outside a git repository.
	CodeNotAGitRepo = "not_a_git_repo"
	// CodeOffline is for a fetch or push that could not reach the remote.
	CodeOffline = "offline"
	// CodePushRejected is for a push the remote refused for a reason that
	// trying again won't change.
	CodePushRejected = "push_rejected"
)

// ErrNoWorktree is WorktreeRoot's error inside a repository but outside any
// working tree (a bare repository, or under .git).
var ErrNoWorktree = errors.New("inside a git repository, but not inside a working tree")

// Repo is a git repository, named by any directory inside it. An empty Dir
// is the process's working directory.
//
// Git gets the process's whole environment, so GIT_DIR, GIT_WORK_TREE and
// the like, when set, name the repository instead of Dir. For the CLI that
// is what a user who sets them means; a caller that names a Dir and runs
// with those set would get the wrong repository. Nothing sets them today.
type Repo struct {
	Dir string
	// NoPrompt keeps git from asking for a username or password on the
	// terminal (GIT_TERMINAL_PROMPT=0), so a caller with no terminal, the
	// session-start hook, fails at once instead of waiting on a prompt
	// nobody sees. Credential helpers, keychains and agents still run.
	NoPrompt bool
}

// waitDelay bounds how long run waits, once git has exited or the context
// has ended, for a child of git's (a credential helper, ssh) that still
// holds git's output pipe. Without it a hook could outlive its deadline.
const waitDelay = 2 * time.Second

// Remote is one configured remote.
type Remote struct {
	Name, URL string
}

// failure is a git process that ran and exited non-zero.
type failure struct {
	args   []string
	code   int
	stderr string
}

func (f *failure) Error() string {
	return fmt.Sprintf("git %s: %s", f.args[0], reason(f.stderr))
}

// run runs one git process in the repository and returns its stdout. A
// non-zero exit is a *failure holding stderr; a context that ended is the
// context's error. Messages are forced to English, since some are read.
func (r Repo) run(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.Dir
	cmd.Env = r.env()
	cmd.WaitDelay = waitDelay
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if errors.Is(err, exec.ErrWaitDelay) {
		// git itself exited 0; a child of its kept the pipe. Its output is in.
		err = nil
	}
	if err == nil {
		return stdout.Bytes(), nil
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("git %s: %w", args[0], ctx.Err())
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return stdout.Bytes(), &failure{args: args, code: exit.ExitCode(), stderr: stderr.String()}
	}
	if errors.Is(err, exec.ErrNotFound) {
		return nil, fmt.Errorf("tess needs git, and can't find it: %w", err)
	}
	return nil, fmt.Errorf("git %s: %w", args[0], err)
}

// env is the environment git runs with: the process's own, with LC_ALL=C
// so git's wording can be read, and the prompt off when NoPrompt is set.
func (r Repo) env() []string {
	env := append(os.Environ(), "LC_ALL=C")
	if r.NoPrompt {
		env = append(env, "GIT_TERMINAL_PROMPT=0")
	}
	return env
}

// UserName is git's user.name as configured for this repository, or ""
// when none is set.
func (r Repo) UserName(ctx context.Context) (string, error) {
	out, err := r.run(ctx, nil, "config", "user.name")
	if exitCode(err) == 1 { // unset
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// exitCode is the exit status of a git process that failed, or 0 for any
// other error.
func exitCode(err error) int {
	var f *failure
	if errors.As(err, &f) {
		return f.code
	}
	return 0
}

// reason is the line of git's stderr that says what went wrong: the first
// fatal or error line, else the first line, without the prefix.
func reason(stderr string) string {
	first := ""
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		for _, prefix := range []string{"fatal: ", "error: "} {
			if rest, ok := strings.CutPrefix(line, prefix); ok {
				return rest
			}
		}
		if first == "" {
			first = line
		}
	}
	if first == "" {
		return "git failed without saying why"
	}
	return first
}

// Version returns the version of the git binary on PATH, as in "2.47.1".
func Version(ctx context.Context) (string, error) {
	out, err := Repo{}.run(ctx, nil, "--version")
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(strings.TrimSpace(string(out)), "git version "), nil
}

// abs makes a path git printed (relative to the repository's Dir, or whole)
// absolute.
func (r Repo) abs(path string) (string, error) {
	if !filepath.IsAbs(path) && r.Dir != "" {
		path = filepath.Join(r.Dir, path)
	}
	return filepath.Abs(path)
}

// CommonDir returns the absolute path of the repository's common directory.
// Every worktree of a repository has the same one. Outside a repository the
// error is a *tracker.Error with CodeNotAGitRepo.
func (r Repo) CommonDir(ctx context.Context) (string, error) {
	out, err := r.run(ctx, nil, "rev-parse", "--git-common-dir")
	if err != nil {
		if exitCode(err) == 0 {
			return "", err
		}
		return "", &tracker.Error{
			Code:    CodeNotAGitRepo,
			Message: "this directory is not inside a git repository",
			Hint:    "run tess inside a repository, or `git init` here first",
		}
	}
	return r.abs(strings.TrimSuffix(string(out), "\n"))
}

// WorktreeRoot returns the top directory of the working tree Dir is in:
// where files meant to be committed go. Inside a repository but outside a
// working tree the error is ErrNoWorktree.
func (r Repo) WorktreeRoot(ctx context.Context) (string, error) {
	out, err := r.run(ctx, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		if exitCode(err) == 0 {
			return "", err
		}
		if _, err := r.CommonDir(ctx); err != nil {
			return "", err
		}
		return "", ErrNoWorktree
	}
	return r.abs(strings.TrimSuffix(string(out), "\n"))
}

// ResolveRef returns the commit a ref points at, and false if the
// repository has no such ref.
func (r Repo) ResolveRef(ctx context.Context, ref string) (string, bool, error) {
	out, err := r.run(ctx, nil, "rev-parse", "--verify", "--quiet", "--end-of-options", ref+"^{commit}")
	if err != nil {
		if exitCode(err) == 1 {
			return "", false, nil
		}
		return "", false, err
	}
	return strings.TrimSpace(string(out)), true, nil
}

// Remotes returns the configured remotes and their URLs, sorted by name.
func (r Repo) Remotes(ctx context.Context) ([]Remote, error) {
	out, err := r.run(ctx, nil, "config", "-z", "--get-regexp", `^remote\..*\.url$`)
	if err != nil {
		if exitCode(err) == 1 { // no key matches
			return nil, nil
		}
		return nil, err
	}
	var remotes []Remote
	seen := map[string]bool{}
	// Each entry is the key, a newline, the value, and a NUL.
	for _, entry := range strings.Split(string(out), "\x00") {
		key, url, ok := strings.Cut(entry, "\n")
		if !ok {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".url")
		if seen[name] { // a remote with several URLs: the first is the one git fetches
			continue
		}
		seen[name] = true
		remotes = append(remotes, Remote{Name: name, URL: url})
	}
	sort.Slice(remotes, func(i, j int) bool { return remotes[i].Name < remotes[j].Name })
	return remotes, nil
}

// UpdateRef points ref at commit, but only if it now points at expected:
// compare-and-swap. An empty expected means the ref must not exist. A ref
// that is somewhere else is left alone and the call fails.
func (r Repo) UpdateRef(ctx context.Context, ref, commit, expected string) error {
	if expected == "" {
		expected = strings.Repeat("0", len(commit))
	}
	_, err := r.run(ctx, nil, "update-ref", "--end-of-options", ref, commit, expected)
	return err
}
