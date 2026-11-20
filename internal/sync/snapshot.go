package sync

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/plongitudes/tesserae/internal/tracker"
)

// Format is the snapshot format this build reads and writes, the "format"
// of config.json.
const Format = 1

// CodeFormatUnsupported is the code for a snapshot in a format this build
// does not know.
const CodeFormatUnsupported = "format_unsupported"

// The directories of the tree (ADR-0011).
const (
	configPath  = "config.json"
	issuesDir   = "issues/"
	commentsDir = "comments/"
	eventsDir   = "events/"
	textsDir    = "texts/"
	jsonSuffix  = ".json"
)

// timeLayout is a timestamp in a snapshot: RFC 3339, UTC, to the second.
const timeLayout = "2006-01-02T15:04:05Z"

// The files as they are written. encoding/json writes a struct's fields in
// the order they are declared, so these declarations are the key order of
// sync.md, "The snapshot".
type (
	configFile struct {
		Format int    `json:"format"`
		Prefix string `json:"prefix"`
	}

	issueFile struct {
		ID                 string   `json:"id"`
		Title              string   `json:"title"`
		Description        string   `json:"description"`
		Design             string   `json:"design"`
		AcceptanceCriteria string   `json:"acceptance_criteria"`
		Notes              string   `json:"notes"`
		Type               string   `json:"type"`
		Priority           int      `json:"priority"`
		Status             string   `json:"status"`
		StatusReason       *string  `json:"status_reason"`
		Assignee           *string  `json:"assignee"`
		Owner              *string  `json:"owner"`
		Labels             []string `json:"labels"`
		Parent             *string  `json:"parent"`
		BlockedBy          []string `json:"blocked_by"`
		Related            []string `json:"related"`
		DiscoveredFrom     []string `json:"discovered_from"`
		DueAt              *string  `json:"due_at"`
		CreatedAt          string   `json:"created_at"`
		CreatedBy          string   `json:"created_by"`
		UpdatedAt          string   `json:"updated_at"`
		RetiredAt          *string  `json:"retired_at"`
	}

	commentFile struct {
		ID        string `json:"id"`
		IssueID   string `json:"issue_id"`
		Author    string `json:"author"`
		Text      string `json:"text"`
		CreatedAt string `json:"created_at"`
	}

	eventFile struct {
		ID        string `json:"id"`
		IssueID   string `json:"issue_id"`
		ChangedAt string `json:"changed_at"`
		ChangedBy string `json:"changed_by"`
		Attr      string `json:"attr"`
		OldValue  any    `json:"old_value"`
		NewValue  any    `json:"new_value"`
	}
)

// Files is the canonical tree of the snapshot, path to bytes: the same
// snapshot gives the same bytes on every copy. Collections are written
// sorted whatever order the snapshot holds them in.
func (s *Snapshot) Files() map[string][]byte {
	files := make(map[string][]byte, 1+len(s.Issues)+len(s.Comments)+len(s.Events)+len(s.Texts))
	files[configPath] = encode(configFile{Format: s.Config.Format, Prefix: s.Config.Prefix})
	for _, i := range s.Issues {
		files[issuesDir+i.ID+jsonSuffix] = encode(issueFile{
			ID: i.ID, Title: i.Title,
			Description: i.Description, Design: i.Design, AcceptanceCriteria: i.AcceptanceCriteria, Notes: i.Notes,
			Type: i.Type, Priority: i.Priority,
			Status: i.Status, StatusReason: i.StatusReason, Assignee: i.Assignee, Owner: i.Owner,
			Labels: sorted(i.Labels), Parent: i.Parent,
			BlockedBy: sorted(i.BlockedBy), Related: sorted(i.Related), DiscoveredFrom: sorted(i.DiscoveredFrom),
			DueAt: formatTimePtr(i.DueAt), CreatedAt: formatTime(i.CreatedAt), CreatedBy: i.CreatedBy,
			UpdatedAt: formatTime(i.UpdatedAt), RetiredAt: formatTimePtr(i.RetiredAt),
		})
	}
	for _, c := range s.Comments {
		files[commentsDir+c.IssueID+"/"+c.ID+jsonSuffix] = commentBytes(c)
	}
	for _, e := range s.Events {
		files[eventsDir+e.IssueID+"/"+e.ID+jsonSuffix] = eventBytes(e)
	}
	for hash, body := range s.Texts {
		files[textsDir+hash] = []byte(body)
	}
	return files
}

// commentBytes is a comment's file.
func commentBytes(c *Comment) []byte {
	return encode(commentFile{
		ID: c.ID, IssueID: c.IssueID, Author: c.Author, Text: c.Text, CreatedAt: formatTime(c.CreatedAt),
	})
}

// eventBytes is an event's file.
func eventBytes(e *Event) []byte {
	return encode(eventFile{
		ID: e.ID, IssueID: e.IssueID, ChangedAt: formatTime(e.ChangedAt), ChangedBy: e.ChangedBy,
		Attr: e.Attr, OldValue: e.OldValue, NewValue: e.NewValue,
	})
}

// formatUnsupported is the refusal of a snapshot in a format this build
// does not know.
func formatUnsupported(format int) *tracker.Error {
	return &tracker.Error{
		Code:    CodeFormatUnsupported,
		Message: fmt.Sprintf("the snapshot is in format %d; this tess understands format %d", format, Format),
		Hint:    "upgrade tess to the version the other copies use",
	}
}

// FromFiles parses a tree written by Files back into a snapshot. It is
// strict: a file or a key it does not know is an error, since writing the
// snapshot out again would silently lose it. A config.json with a format
// other than Format is refused with format_unsupported before anything else
// is read.
func FromFiles(files map[string][]byte) (*Snapshot, error) {
	raw, ok := files[configPath]
	if !ok {
		return nil, fmt.Errorf("snapshot has no %s", configPath)
	}
	// Only the format is read at first: a later format may shape the rest of
	// config.json differently.
	var version struct {
		Format int `json:"format"`
	}
	if err := json.Unmarshal(raw, &version); err != nil {
		return nil, fmt.Errorf("read %s: %w", configPath, err)
	}
	if version.Format != Format {
		return nil, formatUnsupported(version.Format)
	}

	s := &Snapshot{
		Issues:   map[string]*Issue{},
		Comments: map[string]*Comment{},
		Events:   map[string]*Event{},
		Texts:    map[string]string{},
	}
	// In path order, so that which problem is reported first doesn't depend
	// on map order.
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		if err := s.parseFile(path, files[path]); err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
	}
	return s, nil
}

func (s *Snapshot) parseFile(path string, raw []byte) error {
	switch {
	case path == configPath:
		var f configFile
		if err := decode(raw, &f); err != nil {
			return err
		}
		s.Config = Config{Format: f.Format, Prefix: f.Prefix}
		return nil

	case strings.HasPrefix(path, textsDir):
		hash := strings.TrimPrefix(path, textsDir)
		if sum := sha256.Sum256(raw); hex.EncodeToString(sum[:]) != hash {
			return fmt.Errorf("the contents don't hash to the file's name")
		}
		s.Texts[hash] = string(raw)
		return nil

	case strings.HasPrefix(path, issuesDir) && strings.HasSuffix(path, jsonSuffix):
		var f issueFile
		if err := decode(raw, &f); err != nil {
			return err
		}
		if path != issuesDir+f.ID+jsonSuffix {
			return fmt.Errorf("the file holds issue %q", f.ID)
		}
		i := &Issue{
			ID: f.ID, Title: f.Title,
			Description: f.Description, Design: f.Design, AcceptanceCriteria: f.AcceptanceCriteria, Notes: f.Notes,
			Type: f.Type, Priority: f.Priority,
			Status: f.Status, StatusReason: f.StatusReason, Assignee: f.Assignee, Owner: f.Owner,
			Labels: sorted(f.Labels), Parent: f.Parent,
			BlockedBy: sorted(f.BlockedBy), Related: sorted(f.Related), DiscoveredFrom: sorted(f.DiscoveredFrom),
			CreatedBy: f.CreatedBy,
		}
		var err error
		if i.CreatedAt, err = parseTime(f.CreatedAt); err != nil {
			return err
		}
		if i.UpdatedAt, err = parseTime(f.UpdatedAt); err != nil {
			return err
		}
		if i.DueAt, err = parseTimePtr(f.DueAt); err != nil {
			return err
		}
		if i.RetiredAt, err = parseTimePtr(f.RetiredAt); err != nil {
			return err
		}
		s.Issues[i.ID] = i
		return nil

	case strings.HasPrefix(path, commentsDir) && strings.HasSuffix(path, jsonSuffix):
		var f commentFile
		if err := decode(raw, &f); err != nil {
			return err
		}
		if path != commentsDir+f.IssueID+"/"+f.ID+jsonSuffix {
			return fmt.Errorf("the file holds comment %q on issue %q", f.ID, f.IssueID)
		}
		at, err := parseTime(f.CreatedAt)
		if err != nil {
			return err
		}
		s.Comments[f.ID] = &Comment{ID: f.ID, IssueID: f.IssueID, Author: f.Author, Text: f.Text, CreatedAt: at}
		return nil

	case strings.HasPrefix(path, eventsDir) && strings.HasSuffix(path, jsonSuffix):
		var f eventFile
		if err := decode(raw, &f); err != nil {
			return err
		}
		if path != eventsDir+f.IssueID+"/"+f.ID+jsonSuffix {
			return fmt.Errorf("the file holds event %q on issue %q", f.ID, f.IssueID)
		}
		at, err := parseTime(f.ChangedAt)
		if err != nil {
			return err
		}
		s.Events[f.ID] = &Event{
			ID: f.ID, IssueID: f.IssueID, ChangedAt: at, ChangedBy: f.ChangedBy,
			Attr: f.Attr, OldValue: f.OldValue, NewValue: f.NewValue,
		}
		return nil
	}
	return fmt.Errorf("not a file of a format %d snapshot", Format)
}

// encode is one file's bytes: the object with no insignificant whitespace
// and one trailing newline. Strings are written as they are, without the
// HTML escaping encoding/json does by default.
func encode(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		// The file types hold strings, ints and JSON values; a value that
		// can't be encoded is a programming error in whoever built the
		// snapshot.
		panic(fmt.Sprintf("sync: encode %T: %v", v, err))
	}
	return buf.Bytes()
}

// decode reads one JSON object into v, refusing unknown keys and anything
// after the object.
func decode(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("more than one JSON value")
	}
	return nil
}

// sorted is a sorted copy of list, never nil, so that an empty collection
// is written as [] and the caller's slice keeps its order.
func sorted(list []string) []string {
	out := make([]string, len(list))
	copy(out, list)
	slices.Sort(out)
	return out
}

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := formatTime(*t)
	return &s
}

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("timestamp %q: %w", s, err)
	}
	return t.UTC().Truncate(time.Second), nil
}

func parseTimePtr(s *string) (*time.Time, error) {
	if s == nil {
		return nil, nil
	}
	t, err := parseTime(*s)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
