package tracker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"time"

	"github.com/plongitudes/tesserae/internal/store/sqlite"
)

// Codes for a repository's database being there or not.
const (
	CodeNotInitialized     = "not_initialized"
	CodeAlreadyInitialized = "already_initialized"
)

// configPrefix is the config key the ID prefix is kept under. A database is
// initialised when it is set.
const configPrefix = "prefix"

var prefixRE = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

// ValidPrefix reports whether prefix can start an ID: a lowercase letter,
// then lowercase letters and digits.
func ValidPrefix(prefix string) bool { return prefixRE.MatchString(prefix) }

// Init creates the database at path and gives it the prefix new issues' IDs
// start with. A database that already has a prefix is refused with
// already_initialized and left as it is.
func Init(ctx context.Context, path, prefix string) error {
	if !ValidPrefix(prefix) {
		return invalid(
			fmt.Sprintf("%q can't be an ID prefix: it must be a lowercase letter followed by lowercase letters and digits", prefix),
			"choose one with --prefix, e.g. `tess init --prefix proj`")
	}
	store, err := sqlite.Open(ctx, path)
	if err != nil {
		return err
	}
	defer store.Close()
	return store.Write(ctx, func(tx *sql.Tx) error {
		existing, ok, err := sqlite.ConfigIn(ctx, tx, configPrefix)
		if err != nil {
			return err
		}
		if ok {
			return &Error{
				Code:    CodeAlreadyInitialized,
				Message: fmt.Sprintf("this repository already has a tess database, with prefix %q", existing),
				Hint:    "run `tess ready` to see what's in it",
			}
		}
		return sqlite.SetConfig(ctx, tx, configPrefix, prefix)
	})
}

// Open returns a Tracker over the database at path, and the function that
// closes it. now is the clock for every timestamp it writes, and actor is
// who its changes are recorded as. A database that Init hasn't set up is
// refused with not_initialized, and Open never creates one.
func Open(ctx context.Context, path string, now func() time.Time, actor string) (Tracker, func() error, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, nil, NotInitialized()
	} else if err != nil {
		return nil, nil, err
	}
	store, err := sqlite.Open(ctx, path)
	if err != nil {
		return nil, nil, err
	}
	_, ok, err := store.Config(ctx, configPrefix)
	if err == nil && !ok {
		err = NotInitialized()
	}
	if err != nil {
		store.Close()
		return nil, nil, err
	}
	return New(store, now, actor), store.Close, nil
}

// NotInitialized is the error for a repository with no tess database:
// the one wording, used by the tracker and by sync.
func NotInitialized() *Error {
	return &Error{
		Code:    CodeNotInitialized,
		Message: "this repository has no tess database yet; run `tess init` to create one",
		Hint:    "run `tess init`",
	}
}
