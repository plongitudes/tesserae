package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/plongitudes/tesserae/internal/git"
	"github.com/plongitudes/tesserae/internal/tracker"
)

// databaseName is the database's file name inside git's common directory
// (ADR-0003, cli.md).
const databaseName = "tess.db"

// databasePath returns where this repository's database is, or would be.
func databasePath(ctx context.Context) (string, error) {
	dir, err := git.Repo{}.CommonDir(ctx)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, databaseName), nil
}

// clock returns the clock for every timestamp the process writes: TESS_NOW
// if set, and the system clock otherwise (acceptance/README.md, "Clock seam").
func clock() (func() time.Time, error) {
	fixed := os.Getenv("TESS_NOW")
	if fixed == "" {
		return func() time.Time { return time.Now().UTC() }, nil
	}
	at, err := time.Parse(time.RFC3339, fixed)
	if err != nil {
		return nil, invalidArgument(
			fmt.Sprintf("TESS_NOW is %q, which is not an RFC 3339 time", fixed),
			"unset TESS_NOW, or set it like 2026-01-01T00:00:00Z")
	}
	at = at.UTC()
	return func() time.Time { return at }, nil
}

// actor returns who this process's changes are recorded as: TESS_ACTOR; else
// a Claude Code session, as claude:<first 8 of its ID>; else git's user.name
// (ADR-0019, item 2).
func actor(ctx context.Context) string {
	if name := os.Getenv("TESS_ACTOR"); name != "" {
		return name
	}
	if os.Getenv("CLAUDECODE") == "1" {
		session := os.Getenv("CLAUDE_CODE_SESSION_ID")
		if len(session) > 8 {
			session = session[:8]
		}
		return "claude:" + session
	}
	if out, err := exec.CommandContext(ctx, "git", "config", "user.name").Output(); err == nil {
		if name := strings.TrimSpace(string(out)); name != "" {
			return name
		}
	}
	if name := os.Getenv("USER"); name != "" {
		return name
	}
	return "unknown"
}

// withTracker runs fn over this repository's tracker. It is how every
// command but init reaches the database, so not_a_git_repo and
// not_initialized are decided in one place.
func withTracker(ctx context.Context, fn func(tr tracker.Tracker) error) error {
	path, err := databasePath(ctx)
	if err != nil {
		return err
	}
	now, err := clock()
	if err != nil {
		return err
	}
	tr, closeDB, err := tracker.Open(ctx, path, now, actor(ctx))
	if err != nil {
		return err
	}
	defer closeDB()
	return fn(tr)
}

// worktreeRoot returns the top directory of the worktree the working
// directory is in: where files meant to be committed go.
func worktreeRoot(ctx context.Context) (string, error) {
	root, err := git.Repo{}.WorktreeRoot(ctx)
	if errors.Is(err, git.ErrNoWorktree) {
		return "", invalidArgument("this is a git repository, but not inside a working tree", "run this from a checkout of the repository")
	}
	return root, err
}
