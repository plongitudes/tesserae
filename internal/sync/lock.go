package sync

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/plongitudes/tesserae/internal/tracker"
)

// CodeBusy is for a sync that waited for another sync on this machine and
// gave up. It is not recorded in sync.last_result: waiting on a neighbour
// is not a sync that failed.
const CodeBusy = "busy"

// lockWait is how long a sync waits for another one to finish (sync.md,
// "What a sync is").
const lockWait = 30 * time.Second

// lockPoll is how often a waiting sync looks again.
const lockPoll = 50 * time.Millisecond

// LockPath is the lock file of a database: next to it, so every worktree
// of a repository, sharing the database, shares the lock. The holder
// writes its process ID and start time into it, so a waiting sync and
// `tess doctor` can say who is running and for how long.
func LockPath(dbPath string) string { return dbPath + ".sync.lock" }

// LockHolder is what the lock file says about the sync holding it.
type LockHolder struct {
	PID   int
	Since time.Time // by the wall clock, not TESS_NOW: a process fact
}

// Lock takes the sync lock of a database, waiting as a sync would, and
// returns what releases it. A caller that wants to keep a sync from
// running, or a test that wants to be the other sync, uses it.
func Lock(ctx context.Context, dbPath string) (release func(), err error) {
	return acquire(ctx, dbPath, lockWait)
}

// LockHeld reports whether a sync holds the lock of a database right now,
// and what its lock file says about it. A lock file nobody holds, or one
// without the holder's record, is not held.
func LockHeld(dbPath string) (holder LockHolder, held bool, err error) {
	path := LockPath(dbPath)
	if _, err := os.Stat(path); err != nil {
		return LockHolder{}, false, nil
	}
	f, ok, err := tryLock(path)
	if err != nil {
		return LockHolder{}, false, fmt.Errorf("sync lock %s: %w", path, err)
	}
	if ok {
		unlock(f, path)
		return LockHolder{}, false, nil
	}
	holder, _ = readHolder(path)
	return holder, true, nil
}

// acquire takes the sync lock of a database, waiting up to wait for
// whoever holds it. The lock is held until release is called or the
// process ends; on unix the file itself stays. Giving up is a
// *tracker.Error with CodeBusy naming the holder; a context that ends
// first is the context's error.
func acquire(ctx context.Context, dbPath string, wait time.Duration) (release func(), err error) {
	path := LockPath(dbPath)
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	poll := time.NewTicker(lockPoll)
	defer poll.Stop()
	for {
		f, ok, err := tryLock(path)
		if err != nil {
			return nil, fmt.Errorf("sync lock %s: %w", path, err)
		}
		if ok {
			writeHolder(f, LockHolder{PID: os.Getpid(), Since: time.Now()})
			return func() { unlock(f, path) }, nil
		}
		select {
		case <-poll.C:
		case <-deadline.C:
			return nil, &tracker.Error{
				Code:    CodeBusy,
				Message: fmt.Sprintf("another sync of this repository%s did not finish within %s", holderNote(path, time.Now()), wait),
				Hint:    "run `tess sync` again when it is done",
			}
		case <-ctx.Done():
			return nil, fmt.Errorf("waiting for another sync to finish: %w", ctx.Err())
		}
	}
}

// holderNote is " (pid N, running for D)" for the lock file's holder, or
// "" when the file says nothing.
func holderNote(path string, now time.Time) string {
	holder, ok := readHolder(path)
	if !ok {
		return ""
	}
	return fmt.Sprintf(" (pid %d, running for %s)", holder.PID, now.Sub(holder.Since).Round(time.Second))
}

// writeHolder records the holder in the lock file: two lines, `pid N` and
// `since <RFC 3339>`. The file is truncated first, so a stale record from
// an earlier holder never shows.
func writeHolder(f *os.File, h LockHolder) {
	// Best effort: the lock is held whether or not the record is written.
	if err := f.Truncate(0); err != nil {
		return
	}
	_, _ = f.WriteAt([]byte(fmt.Sprintf("pid %d\nsince %s\n", h.PID, h.Since.UTC().Format(time.RFC3339))), 0)
}

// readHolder reads what writeHolder wrote. Anything else is no record.
func readHolder(path string) (LockHolder, bool) {
	f, err := os.Open(path)
	if err != nil {
		return LockHolder{}, false
	}
	defer f.Close()
	var h LockHolder
	var pid, since bool
	for s := bufio.NewScanner(f); s.Scan(); {
		key, value, _ := strings.Cut(s.Text(), " ")
		switch key {
		case "pid":
			if n, err := strconv.Atoi(value); err == nil {
				h.PID, pid = n, true
			}
		case "since":
			if at, err := time.Parse(time.RFC3339, value); err == nil {
				h.Since, since = at, true
			}
		}
	}
	return h, pid && since
}
