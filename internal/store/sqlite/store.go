// Package sqlite is the store: the SQLite schema, the readiness views, and
// transactions. "Blocked" is defined here once, as the blocked_issues view
// (ADR-0007); nothing in Go reimplements it.
package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"time"

	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/driver"
)

// SchemaVersion is the version of the schema this build creates and expects,
// recorded in the config table under "schema_version".
const SchemaVersion = 1

//go:embed schema.sql
var schemaSQL string

const (
	// How long SQLite itself waits on a lock before reporting SQLITE_BUSY.
	busyTimeout = 5 * time.Second
	// How long Write keeps retrying a BEGIN that still came back busy.
	busyRetryFor = 30 * time.Second
)

// Store is one connection to a tesserae database.
type Store struct {
	db *sql.DB
}

// Open opens the database at path, creating the file and the schema if they
// don't exist yet. path may be ":memory:" for a private in-memory database.
//
// Several processes may open the same file: the database is in WAL mode and
// every write transaction is BEGIN IMMEDIATE with busy retry (ADR-0003).
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := driver.Open(path, func(c *sqlite3.Conn) error {
		// The busy timeout comes first so the other pragmas wait on locks too.
		if err := c.BusyTimeout(busyTimeout); err != nil {
			return err
		}
		return c.Exec(`PRAGMA journal_mode = WAL; PRAGMA synchronous = NORMAL; PRAGMA foreign_keys = ON`)
	})
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	// One connection: a tess process runs one command, and an in-memory
	// database exists only on the connection that created it.
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return s, nil
}

// Close releases the connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// Write runs fn in a write transaction, committing if fn returns nil and
// rolling back otherwise. The transaction takes the write lock up front
// (BEGIN IMMEDIATE), so fn never fails half-way on a lock, and waiting for
// another writer is retried here instead of surfacing SQLITE_BUSY.
func (s *Store) Write(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.beginImmediate(ctx)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Read runs fn in a read-only transaction: one consistent snapshot, and any
// attempt to write fails.
func (s *Store) Read(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return fn(tx)
}

// beginImmediate starts a BEGIN IMMEDIATE transaction. SQLite's busy timeout
// does most of the waiting; a BEGIN that still reports busy is retried with
// backoff until busyRetryFor has passed.
func (s *Store) beginImmediate(ctx context.Context) (*sql.Tx, error) {
	deadline := time.Now().Add(busyRetryFor)
	backoff := 5 * time.Millisecond
	for {
		// The driver's default lock mode is "immediate"; serializable pins it
		// whatever the data source name says.
		tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
		if err == nil {
			return tx, nil
		}
		if !errors.Is(err, sqlite3.BUSY) || time.Now().After(deadline) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 200*time.Millisecond {
			backoff *= 2
		}
	}
}

// migrate creates the schema in a new database and checks the version of an
// existing one. It runs in a write transaction, so two processes opening a
// new file at once create the schema exactly once.
func (s *Store) migrate(ctx context.Context) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		var exists bool
		err := tx.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'config')`).Scan(&exists)
		if err != nil {
			return err
		}
		if !exists {
			if _, err := tx.ExecContext(ctx, schemaSQL); err != nil {
				return fmt.Errorf("create schema: %w", err)
			}
			return SetConfig(ctx, tx, "schema_version", fmt.Sprint(SchemaVersion))
		}
		var version string
		err = tx.QueryRowContext(ctx, `SELECT value FROM config WHERE key = 'schema_version'`).Scan(&version)
		if err != nil {
			return fmt.Errorf("read schema version: %w", err)
		}
		if version != fmt.Sprint(SchemaVersion) {
			return fmt.Errorf("database has schema version %s; this tess understands version %d", version, SchemaVersion)
		}
		return nil
	})
}

// Config returns the value stored under key in the config table, and whether
// the key is set.
func (s *Store) Config(ctx context.Context, key string) (value string, ok bool, err error) {
	err = s.Read(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT value FROM config WHERE key = ?`, key).Scan(&value)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return value, err == nil, err
}

// SetConfig sets key to value in the config table, inside the caller's write
// transaction.
func SetConfig(ctx context.Context, tx *sql.Tx, key, value string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO config (key, value) VALUES (?, ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
