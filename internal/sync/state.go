package sync

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"github.com/plongitudes/tesserae/internal/git"
	"github.com/plongitudes/tesserae/internal/store/sqlite"

	"github.com/plongitudes/tesserae/internal/sync/gitref"
)

// State is what a copy can say about itself and its syncing without
// reaching a remote: what `tess doctor` and the session-start facts report.
type State struct {
	Schema int    // the database's schema version
	Prefix string // the copy's ID prefix
	Issues int    // the issues it holds, not counting retired ones

	// LastAt is the time of the last sync that worked; zero when the copy
	// has never synced.
	LastAt time.Time
	// LastResult is ResultOK, the code the last sync failed with, or ""
	// when no sync has been tried.
	LastResult string
	// HasLKG reports whether the local ref, the last known good snapshot,
	// is there.
	HasLKG bool
	// Changed is the number of issues that differ from the last known good
	// snapshot: what the next sync would push. Without one, every issue.
	Changed int
	// Running is the sync holding the lock right now, if any.
	Running *LockHolder
}

// ReadState reads the state of the copy whose database is at dbPath, in
// the repository dir is in. It writes nothing and uses no network. A
// database that `tess init` has not set up is refused with not_initialized.
func ReadState(ctx context.Context, dir, dbPath string) (*State, error) {
	store, err := openExisting(ctx, dbPath)
	if err != nil {
		return nil, err
	}
	defer store.Close()

	var snap *Snapshot
	config := map[string]string{}
	err = store.Read(ctx, func(tx *sql.Tx) error {
		if snap, err = loadIn(ctx, tx); err != nil {
			return err
		}
		for _, key := range []string{"schema_version", ConfigLastAt, ConfigLastResult} {
			value, _, err := sqlite.ConfigIn(ctx, tx, key)
			if err != nil {
				return err
			}
			config[key] = value
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	state := &State{Prefix: snap.Config.Prefix, LastResult: config[ConfigLastResult]}
	if state.Schema, err = strconv.Atoi(config["schema_version"]); err != nil {
		return nil, fmt.Errorf("read schema version: %w", err)
	}
	for _, issue := range snap.Issues {
		if issue.RetiredAt == nil {
			state.Issues++
		}
	}
	if at := config[ConfigLastAt]; at != "" {
		if state.LastAt, err = parseTime(at); err != nil {
			return nil, fmt.Errorf("read %s: %w", ConfigLastAt, err)
		}
	}
	// The local ref is listed, not read: its blob IDs against the hashes of
	// the snapshot's files say which issues changed, with no cat-file.
	commit, ids, err := gitref.LocalIDs(ctx, dir)
	if err != nil {
		return nil, err
	}
	format, err := (git.Repo{Dir: dir}).ObjectFormat(ctx)
	if err != nil {
		return nil, err
	}
	state.HasLKG = commit != ""
	state.Changed = issuesChangedIDs(ids, snap.Files(), func(data []byte) string { return git.HashBlob(format, data) })
	if holder, held, err := LockHeld(dbPath); err != nil {
		return nil, err
	} else if held {
		state.Running = &holder
	}
	return state, nil
}

// FindRemote names the remote a sync in dir would use: the one named, else
// origin, else the only remote (sync.md, "Git"); no_remote when that picks
// none. With reach it also asks the remote whether it is there (one
// ls-remote, no fetch), the one step here that uses the network, and a
// remote that can't be reached is offline. A remote that holds no issues
// yet is reachable.
func FindRemote(ctx context.Context, dir, name string, reach bool) (string, error) {
	transport, err := gitref.Open(ctx, git.Repo{Dir: dir}, name)
	if err != nil {
		return "", err
	}
	if reach {
		if err := transport.Reachable(ctx); err != nil {
			return transport.Remote(), err
		}
	}
	return transport.Remote(), nil
}
