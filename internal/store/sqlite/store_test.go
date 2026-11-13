package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func openMemory(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:): %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenCreatesSchemaAndRecordsItsVersion(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	got, ok, err := s.Config(ctx, "schema_version")
	if err != nil || !ok || got != "1" {
		t.Fatalf("schema_version = %q, %v, %v; want \"1\", true, nil", got, ok, err)
	}
	if _, ok, err := s.Config(ctx, "prefix"); err != nil || ok {
		t.Fatalf("prefix set on a fresh store: ok=%v err=%v", ok, err)
	}
}

func TestConfigIsWrittenInsideAWriteTransaction(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	err := s.Write(ctx, func(tx *sql.Tx) error { return SetConfig(ctx, tx, "prefix", "tess") })
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got, ok, err := s.Config(ctx, "prefix"); err != nil || !ok || got != "tess" {
		t.Fatalf("prefix = %q, %v, %v; want \"tess\", true, nil", got, ok, err)
	}
}

func TestWriteRollsBackWhenTheFunctionFails(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	boom := errors.New("boom")

	err := s.Write(ctx, func(tx *sql.Tx) error {
		if err := SetConfig(ctx, tx, "prefix", "tess"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Write error = %v, want boom", err)
	}
	if _, ok, _ := s.Config(ctx, "prefix"); ok {
		t.Fatal("prefix survived a failed transaction")
	}
}

func TestReopeningAFileKeepsItsDataAndUsesWAL(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tess.db")

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Write(ctx, func(tx *sql.Tx) error { return SetConfig(ctx, tx, "prefix", "tess") }); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s, err = Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s.Close()
	if got, _, _ := s.Config(ctx, "prefix"); got != "tess" {
		t.Fatalf("prefix after reopen = %q, want tess", got)
	}
	var mode string
	err = s.Read(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode)
	})
	if err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q, %v; want wal", mode, err)
	}
}

// Two stores on one file stand in for two tess processes in separate
// worktrees (ADR-0003). Every write must land; none may surface SQLITE_BUSY.
func TestTwoConnectionsWriteConcurrentlyWithoutBusy(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tess.db")
	const writers, perWriter = 2, 150

	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			// Each writer opens its own store, so creating the schema races too.
			s, err := Open(ctx, path)
			if err != nil {
				errs <- fmt.Errorf("writer %d open: %w", w, err)
				return
			}
			defer s.Close()
			for i := 0; i < perWriter; i++ {
				err := s.Write(ctx, func(tx *sql.Tx) error {
					// Read then write inside the transaction: the shape that
					// deadlocks into SQLITE_BUSY under a deferred BEGIN.
					var n int
					if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM issues").Scan(&n); err != nil {
						return err
					}
					_, err := tx.ExecContext(ctx,
						`INSERT INTO issues (id, title, created_at, created_by, updated_at)
						 VALUES (?, 'x', '2026-01-01T00:00:00Z', 'tester', '2026-01-01T00:00:00Z')`,
						fmt.Sprintf("t-%d-%d", w, i))
					return err
				})
				if err != nil {
					errs <- fmt.Errorf("writer %d write %d: %w", w, i, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	var n int
	if err := s.Read(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT count(*) FROM issues").Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	if n != writers*perWriter {
		t.Fatalf("issues written = %d, want %d", n, writers*perWriter)
	}
}
