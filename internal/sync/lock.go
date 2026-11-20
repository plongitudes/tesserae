package sync

import (
	"context"
	"fmt"
	"time"

	"github.com/plongitudes/tesserae/internal/tracker"
)

// CodeBusy is for a sync that waited for another sync on this machine and
// gave up.
const CodeBusy = "busy"

// lockWait is how long a sync waits for another one to finish (sync.md,
// "What a sync is").
const lockWait = 30 * time.Second

// lockPoll is how often a waiting sync looks again.
const lockPoll = 50 * time.Millisecond

// lockPath is the lock file of a database: next to it, so every worktree
// of a repository, sharing the database, shares the lock.
func lockPath(dbPath string) string { return dbPath + ".sync.lock" }

// acquire takes the sync lock of a database, waiting up to wait for
// whoever holds it. The lock is held until release is called or the
// process ends; the file itself stays. Giving up is a *tracker.Error with
// CodeBusy; a context that ends first is the context's error.
func acquire(ctx context.Context, dbPath string, wait time.Duration) (release func(), err error) {
	path := lockPath(dbPath)
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	poll := time.NewTicker(lockPoll)
	defer poll.Stop()
	for {
		release, ok, err := tryLock(path)
		if err != nil {
			return nil, fmt.Errorf("sync lock %s: %w", path, err)
		}
		if ok {
			return release, nil
		}
		select {
		case <-poll.C:
		case <-deadline.C:
			return nil, &tracker.Error{
				Code:    CodeBusy,
				Message: fmt.Sprintf("another sync of this repository is running and did not finish within %s", wait),
				Hint:    "run `tess sync` again when it is done",
			}
		case <-ctx.Done():
			return nil, fmt.Errorf("waiting for another sync to finish: %w", ctx.Err())
		}
	}
}
