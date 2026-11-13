package tracker

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/plongitudes/tesserae/internal/store/sqlite"
)

func (t *tracker) Retire(ctx context.Context, partialIDs []string, reason string) (Change, error) {
	if len(partialIDs) == 0 {
		return Change{}, invalid("name at least one issue", "")
	}
	var change Change
	err := t.store.Write(ctx, func(tx *sql.Tx) error {
		blockedBefore, err := sqlite.BlockedIssues(ctx, tx)
		if err != nil {
			return err
		}
		now := t.now()
		var retired []string
		for _, partial := range partialIDs {
			id, already, err := resolve(ctx, tx, partial)
			if err != nil {
				return err
			}
			// An issue named twice is retired once; one that was retired
			// before this command is refused like any other change to it.
			if slices.Contains(retired, id) {
				continue
			}
			if already {
				return retiredError(id)
			}
			if err := sqlite.UpdateIssue(ctx, tx, id, sqlite.Patch{RetiredAt: &now}, now, t.actor); err != nil {
				return err
			}
			if strings.TrimSpace(reason) != "" {
				if _, err := sqlite.InsertComment(ctx, tx, id, t.actor, "Retired: "+reason, now); err != nil {
					return err
				}
			}
			retired = append(retired, id)
		}
		unblocked, err := sqlite.NewlyUnblocked(ctx, tx, blockedBefore)
		if err != nil {
			return err
		}
		if change.Updated, err = readAll(ctx, tx, retired); err != nil {
			return err
		}
		change.NewlyUnblocked, err = readAll(ctx, tx, unblocked)
		return err
	})
	if err != nil {
		return Change{}, err
	}
	return change, nil
}

func (t *tracker) Restore(ctx context.Context, id string) (Issue, error) {
	var issue Issue
	err := t.store.Write(ctx, func(tx *sql.Tx) error {
		found, ok, err := sqlite.GetIssue(ctx, tx, id)
		if err != nil {
			return err
		}
		if !ok {
			return &Error{
				Code:    CodeNotFound,
				Message: fmt.Sprintf("no issue has the ID %q", id),
				Hint:    "restore takes the exact ID; tess list --retired shows the retired issues",
			}
		}
		// Restoring an issue that isn't retired changes nothing.
		if found.RetiredAt != nil {
			if err := sqlite.UpdateIssue(ctx, tx, id, sqlite.Patch{RetiredAt: &time.Time{}}, t.now(), t.actor); err != nil {
				return err
			}
		}
		issue, err = read(ctx, tx, id)
		return err
	})
	return issue, err
}

func (t *tracker) Comment(ctx context.Context, partialID, text string) (Comment, error) {
	if strings.TrimSpace(text) == "" {
		return Comment{}, invalid("a comment needs some text", "")
	}
	var comment Comment
	err := t.store.Write(ctx, func(tx *sql.Tx) error {
		id, err := resolveLive(ctx, tx, partialID)
		if err != nil {
			return err
		}
		comment, err = sqlite.InsertComment(ctx, tx, id, t.actor, text, t.now())
		return err
	})
	return comment, err
}
