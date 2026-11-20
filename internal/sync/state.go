package sync

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"

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
	lkg, err := gitref.Local(ctx, dir)
	if err != nil {
		return nil, err
	}
	state.HasLKG = lkg.Present()
	state.Changed = issuesChanged(lkg.Files, snap.Files())
	return state, nil
}

// FindRemote names the remote a sync in dir would use: the one named, else
// origin, else the only remote (sync.md, "Git"); no_remote when that picks
// none. With reach it also fetches from it, the one step here that uses
// the network, and a remote that can't be reached is offline. A remote
// that holds no issues yet is reachable.
func FindRemote(ctx context.Context, dir, name string, reach bool) (string, error) {
	transport, err := gitref.Open(ctx, dir, name)
	if err != nil {
		return "", err
	}
	if reach {
		if _, err := transport.Fetch(ctx); err != nil {
			return transport.Remote(), err
		}
	}
	return transport.Remote(), nil
}
