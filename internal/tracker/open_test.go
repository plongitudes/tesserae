package tracker_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/plongitudes/tesserae/internal/tracker"
)

func codeOf(err error) string {
	var te *tracker.Error
	if errors.As(err, &te) {
		return te.Code
	}
	return ""
}

func fixedClock() time.Time { return start }

func TestOpeningADatabaseThatWasNeverInitialisedIsRefused(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tess.db")
	_, _, err := tracker.Open(ctx, path, fixedClock, "tester")
	if codeOf(err) != tracker.CodeNotInitialized {
		t.Fatalf("Open before Init: got %v, want not_initialized", err)
	}
	// Refusing must not leave a database behind that a later Open would accept.
	_, _, err = tracker.Open(ctx, path, fixedClock, "tester")
	if codeOf(err) != tracker.CodeNotInitialized {
		t.Fatalf("second Open before Init: got %v, want not_initialized", err)
	}
}

func TestInitSetsThePrefixNewIssuesAreNamedWith(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tess.db")
	if err := tracker.Init(ctx, path, "proj"); err != nil {
		t.Fatal(err)
	}
	tr, closeFn, err := tracker.Open(ctx, path, fixedClock, "tester")
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	issue, err := tr.Create(ctx, tracker.NewIssue{Title: "First"})
	if err != nil {
		t.Fatal(err)
	}
	if got := issue.ID[:5]; got != "proj-" {
		t.Errorf("ID %q does not start with proj-", issue.ID)
	}
	if issue.CreatedBy != "tester" || !issue.CreatedAt.Equal(start) {
		t.Errorf("created by %q at %v; want tester at %v", issue.CreatedBy, issue.CreatedAt, start)
	}
}

func TestInitTwiceIsRefusedAndKeepsTheFirstPrefix(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tess.db")
	if err := tracker.Init(ctx, path, "one"); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Init(ctx, path, "two"); codeOf(err) != tracker.CodeAlreadyInitialized {
		t.Fatalf("second Init: got %v, want already_initialized", err)
	}
	tr, closeFn, err := tracker.Open(ctx, path, fixedClock, "tester")
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	issue, err := tr.Create(ctx, tracker.NewIssue{Title: "First"})
	if err != nil {
		t.Fatal(err)
	}
	if got := issue.ID[:4]; got != "one-" {
		t.Errorf("ID %q does not start with one-", issue.ID)
	}
}

func TestInitRefusesAPrefixThatCannotStartAnID(t *testing.T) {
	ctx := context.Background()
	for _, prefix := range []string{"", "9lives", "My", "a-b", "a b", "a.b"} {
		path := filepath.Join(t.TempDir(), "tess.db")
		if err := tracker.Init(ctx, path, prefix); codeOf(err) != tracker.CodeInvalidArgument {
			t.Errorf("Init with prefix %q: got %v, want invalid_argument", prefix, err)
		}
		if _, _, err := tracker.Open(ctx, path, fixedClock, "tester"); codeOf(err) != tracker.CodeNotInitialized {
			t.Errorf("after a refused Init with %q, Open: got %v, want not_initialized", prefix, err)
		}
	}
}
