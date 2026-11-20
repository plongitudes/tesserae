// Package sync exchanges issues between copies of a tracker over a git ref
// (docs/design/sync.md). This is the loop-2 starting point: the exported
// API the acceptance simulator (acceptance/sim) and the CLI use, with
// nothing behind it yet. The loop fills it in.
package sync

import (
	"context"
	"time"

	"github.com/plongitudes/tesserae/internal/tracker"
)

// Snapshot is one copy's state as exchanged by sync (sync.md, "The
// snapshot"). Texts are held as hashes on the issue, bodies in Texts.
type Snapshot struct {
	Config   Config
	Issues   map[string]*Issue   // by issue ID
	Comments map[string]*Comment // by comment ID
	Events   map[string]*Event   // by event ID
	Texts    map[string]string   // by hash
}

// Config is config.json: the format number and the prefix.
type Config struct {
	Format int
	Prefix string
}

// Issue is issues/<id>.json: the stored state of one issue, each link on
// the issue whose event log records it, and related on the smaller ID.
type Issue struct {
	ID, Title, Description, Design, AcceptanceCriteria, Notes, Type string
	Priority                                                        int
	Status                                                          string
	StatusReason, Assignee, Owner                                   *string
	Labels                                                          []string
	Parent                                                          *string
	BlockedBy, Related, DiscoveredFrom                              []string
	DueAt                                                           *time.Time
	CreatedAt                                                       time.Time
	CreatedBy                                                       string
	UpdatedAt                                                       time.Time
	RetiredAt                                                       *time.Time
}

// Comment is comments/<issue>/<id>.json.
type Comment struct {
	ID, IssueID, Author, Text string
	CreatedAt                 time.Time
}

// Event is events/<issue>/<id>.json (ADR-0013).
type Event struct {
	ID, IssueID        string
	ChangedAt          time.Time
	ChangedBy, Attr    string
	OldValue, NewValue any
}

// Resolution is one decision the merge made where the two sides differed
// and both had a say (acceptance/README.md, "Sync output").
type Resolution struct {
	Issue, Field  string
	Kept, Dropped any
	Won, Reason   string
}

// Options is everything Run needs: the repository (any directory inside
// it), the database, the remote, and the clock and actor for the commit.
type Options struct {
	Dir, DBPath, Remote string
	DryRun              bool
	Now                 func() time.Time
	Actor               string
}

// Result is what a sync did (acceptance/README.md, "Sync output").
type Result struct {
	Remote          string
	Fetched, Pushed int
	Resolved        []Resolution
	DryRun          bool
}

// Files is the canonical tree of the snapshot, path to bytes: the same
// snapshot gives the same bytes on every copy.
func (s *Snapshot) Files() map[string][]byte { return nil }

// Merge is the pure 3-way merge of sync.md, "The merge". A nil lkg or
// theirs is an empty snapshot.
func Merge(lkg, ours, theirs *Snapshot) (*Snapshot, []Resolution, error) {
	return nil, nil, notBuilt()
}

// Load reads a database as a snapshot.
func Load(ctx context.Context, dbPath string) (*Snapshot, error) {
	return nil, notBuilt()
}

// Run is the whole sync: fetch, merge, push with retry, store, move the
// local ref. Errors are *tracker.Error with the codes of sync.md.
func Run(ctx context.Context, o Options) (*Result, error) {
	return nil, notBuilt()
}

func notBuilt() error {
	return &tracker.Error{
		Code:    tracker.CodeInvalidArgument,
		Message: "sync is not built yet",
		Hint:    "loop 2 builds it (docs/design/sync.md)",
	}
}
