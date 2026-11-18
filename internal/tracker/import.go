package tracker

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/plongitudes/tesserae/internal/store/sqlite"
)

// ImportResult counts what an import did: the issues it added, and the
// records it left out because an issue with that ID was already there.
type ImportResult struct {
	Imported int `json:"imported"`
	Skipped  int `json:"skipped"`
}

// unknownActor stands in where an old record doesn't say who.
const unknownActor = "unknown"

// bdIssue is one line of the old tool's issues.jsonl, as far as tess keeps
// it. Everything else in the record (Gastown fields, compaction, external
// refs) is dropped.
type bdIssue struct {
	ID                 string         `json:"id"`
	Title              string         `json:"title"`
	Description        string         `json:"description"`
	Design             string         `json:"design"`
	AcceptanceCriteria string         `json:"acceptance_criteria"`
	Notes              string         `json:"notes"`
	Status             string         `json:"status"`
	Priority           int            `json:"priority"`
	IssueType          string         `json:"issue_type"`
	Assignee           string         `json:"assignee"`
	Owner              string         `json:"owner"`
	CreatedAt          time.Time      `json:"created_at"`
	CreatedBy          string         `json:"created_by"`
	UpdatedAt          time.Time      `json:"updated_at"`
	CloseReason        string         `json:"close_reason"`
	ClosedAt           *time.Time     `json:"closed_at"`
	ClosedBy           string         `json:"closed_by"`
	DueAt              *time.Time     `json:"due_at"`
	DeferUntil         *time.Time     `json:"defer_until"`
	DeletedAt          *time.Time     `json:"deleted_at"`
	DeletedBy          string         `json:"deleted_by"`
	DeleteReason       string         `json:"delete_reason"`
	Labels             []string       `json:"labels"`
	Dependencies       []bdDependency `json:"dependencies"`
	Comments           []bdComment    `json:"comments"`
}

// bdDependency is a link as the old tool held it: on the issue that depends,
// naming the issue depended on.
type bdDependency struct {
	DependsOnID string `json:"depends_on_id"`
	Type        string `json:"type"`
}

type bdComment struct {
	Author    string    `json:"author"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

// Import adds the issues of an old bd issues.jsonl, keeping their IDs. A
// record whose ID is already here is skipped whole, links included; nothing
// is ever overwritten. It is all or nothing: any error, a cycle among them,
// leaves the tracker as it was. Like creation, it logs no events: an imported
// issue's history starts here.
func (t *tracker) Import(ctx context.Context, r io.Reader) (ImportResult, error) {
	records, err := readBD(r)
	if err != nil {
		return ImportResult{}, err
	}
	now := t.now()
	var result ImportResult
	err = t.store.Write(ctx, func(tx *sql.Tx) error {
		result = ImportResult{}
		var added []bdIssue
		for _, rec := range records {
			// This also catches an ID that the file itself holds twice: the
			// first record wins.
			exists, err := sqlite.IssueExists(ctx, tx, rec.ID)
			if err != nil {
				return err
			}
			if exists {
				result.Skipped++
				continue
			}
			if err := importIssue(ctx, tx, rec, now, t.actor); err != nil {
				return err
			}
			added = append(added, rec)
			result.Imported++
		}
		// Links go in once every issue is there, since a record can depend on
		// one further down the file.
		for _, rec := range added {
			for _, dep := range rec.Dependencies {
				if err := importLink(ctx, tx, rec.ID, dep); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return ImportResult{}, err
	}
	return result, nil
}

// readBD parses the file, one JSON object per line of any length, and checks
// each record for what an issue can't be without.
func readBD(r io.Reader) ([]bdIssue, error) {
	var records []bdIssue
	in := bufio.NewReader(r)
	for line := 1; ; line++ {
		raw, readErr := in.ReadBytes('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, fmt.Errorf("read the import: %w", readErr)
		}
		if raw = bytes.TrimSpace(raw); len(raw) > 0 {
			var rec bdIssue
			if err := json.Unmarshal(raw, &rec); err != nil {
				return nil, invalid(fmt.Sprintf("line %d is not a bd issue: %v", line, err),
					"import takes bd's issues.jsonl: one JSON object per line")
			}
			if err := checkBD(rec); err != nil {
				return nil, invalid(fmt.Sprintf("line %d: %v", line, err), "")
			}
			records = append(records, rec)
		}
		if readErr != nil {
			return records, nil
		}
	}
}

func checkBD(rec bdIssue) error {
	switch {
	case rec.ID == "":
		return errors.New("the issue has no id")
	case strings.ContainsFunc(rec.ID, func(r rune) bool { return r <= ' ' }):
		return fmt.Errorf("the id %q has a space in it", rec.ID)
	case strings.TrimSpace(rec.Title) == "":
		return fmt.Errorf("%s has no title", rec.ID)
	case rec.Priority < 0 || rec.Priority > 4:
		return fmt.Errorf("%s has priority %d; priorities run from 0 to 4", rec.ID, rec.Priority)
	}
	return nil
}

// importIssue writes one record as an issue, with its labels and comments,
// and one status event where the old tool recorded a change of state
// (acceptance/README.md, "Import reasons"). actor is who is importing.
func importIssue(ctx context.Context, tx *sql.Tx, rec bdIssue, now time.Time, actor string) error {
	row := sqlite.NewIssue{
		ID: rec.ID, Title: rec.Title, Description: rec.Description, Design: rec.Design,
		AcceptanceCriteria: rec.AcceptanceCriteria, Notes: rec.Notes,
		Type: rec.IssueType, Priority: rec.Priority, Assignee: rec.Assignee, Owner: rec.Owner,
		CreatedAt: rec.CreatedAt, CreatedBy: rec.CreatedBy, UpdatedAt: rec.UpdatedAt,
	}
	// The old tool had many more types (messages, gates, agents, ...); what
	// tess has no type for comes in as a task.
	if !slices.Contains(IssueTypes, row.Type) {
		row.Type = IssueTypes[0]
	}
	if row.CreatedBy == "" {
		row.CreatedBy = unknownActor
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = now
	}
	if row.UpdatedAt.IsZero() {
		row.UpdatedAt = row.CreatedAt
	}
	if rec.DueAt != nil {
		row.DueAt = *rec.DueAt
	}
	for _, label := range rec.Labels {
		if strings.TrimSpace(label) != "" {
			row.Labels = append(row.Labels, label)
		}
	}

	comments := slices.Clone(rec.Comments)
	row.Status, row.StatusReason = importStatus(rec)
	if rec.Status == "tombstone" {
		row.RetiredAt = row.UpdatedAt
		if rec.DeletedAt != nil && !rec.DeletedAt.IsZero() {
			row.RetiredAt = *rec.DeletedAt
		}
		// Where retire keeps its reason: a comment on the retired issue.
		if strings.TrimSpace(rec.DeleteReason) != "" {
			comments = append(comments, bdComment{
				Author: rec.DeletedBy, Text: retireComment(rec.DeleteReason), CreatedAt: row.RetiredAt,
			})
		}
	}
	if err := sqlite.InsertIssue(ctx, tx, row); err != nil {
		return err
	}
	if err := importStatusEvent(ctx, tx, rec, row, actor); err != nil {
		return err
	}

	for i := range comments {
		if comments[i].CreatedAt.IsZero() {
			comments[i].CreatedAt = row.CreatedAt
		}
		if comments[i].Author == "" {
			comments[i].Author = unknownActor
		}
	}
	// Comments read back by created_at; those made in one instant keep the
	// file's order, since each ID sorts after the one before.
	for _, c := range comments {
		if _, err := sqlite.InsertComment(ctx, tx, rec.ID, c.Author, c.Text, c.CreatedAt); err != nil {
			return err
		}
	}
	return nil
}

// importStatusEvent gives an imported issue the one event its old record
// vouches for: ready → done at closed_at (else updated_at) for a closed
// issue, by closed_by when the record says; ready → held at updated_at for
// a held one. Nothing for open, in-progress, or tombstones. ready as the old
// value is a guess: the old tool kept no earlier status.
func importStatusEvent(ctx context.Context, tx *sql.Tx, rec bdIssue, row sqlite.NewIssue, actor string) error {
	if rec.Status == "tombstone" {
		return nil
	}
	at, by := row.UpdatedAt, actor
	switch row.Status {
	case "done":
		if rec.ClosedAt != nil && !rec.ClosedAt.IsZero() {
			at = *rec.ClosedAt
		}
		if rec.ClosedBy != "" {
			by = rec.ClosedBy
		}
	case "held":
	default:
		return nil
	}
	return sqlite.InsertStatusEvent(ctx, tx, rec.ID, at, by, "ready", row.Status)
}

// importStatus maps an old status to a stored status and its reason
// (ADR-0009; acceptance/README.md, "Import reasons"). A tombstone has no
// status of its own left, so it is retired as ready. Any status tess has no
// equal for is held, saying which it was.
func importStatus(rec bdIssue) (status, reason string) {
	switch rec.Status {
	case "", "open", "tombstone":
		return "ready", ""
	case "in_progress", "hooked":
		return "in-progress", ""
	case "closed":
		return "done", rec.CloseReason
	case "deferred":
		if rec.DeferUntil != nil && !rec.DeferUntil.IsZero() {
			return "held", "deferred until " + rec.DeferUntil.UTC().Format(time.DateOnly)
		}
	}
	return "held", "imported from bd status " + rec.Status
}

// importLink writes one of a record's dependencies as a link, held to the
// rules Link holds one to (admitLink): one parent, no loops. A link that is
// already there (the old file can say `related` from both ends, or map two
// types onto one) is left alone.
func importLink(ctx context.Context, tx *sql.Tx, id string, dep bdDependency) error {
	target := dep.DependsOnID
	var link Link
	switch dep.Type {
	case "blocks":
		link = Link{From: target, Type: sqlite.LinkBlocks, To: id}
	case "parent-child":
		link = Link{From: target, Type: sqlite.LinkParent, To: id}
	case "discovered-from":
		link = Link{From: id, Type: sqlite.LinkDiscoveredFrom, To: target}
	default: // related, duplicates, supersedes, and the rest of the old types
		link = Link{From: id, Type: sqlite.LinkRelated, To: target}
	}
	if exists, err := sqlite.IssueExists(ctx, tx, target); err != nil {
		return err
	} else if !exists {
		return invalid(
			fmt.Sprintf("%s has a %s link to %q, which is neither in the file nor in the tracker", id, dep.Type, target),
			"take the dependency out of that record, or import the issue it names first")
	}
	_, ok, err := admitLink(ctx, tx, link)
	var refusal *Error
	if errors.As(err, &refusal) && refusal.Code == CodeHasParent {
		// The issue is new, so its other parent is also from the file.
		refusal.Hint = "an issue has one parent; take one parent-child dependency out of that record"
	}
	if err != nil || ok {
		return err
	}
	return sqlite.InsertLinkUnlogged(ctx, tx, link)
}
