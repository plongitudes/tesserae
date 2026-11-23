// Package sync exchanges issues between copies of a tracker over a git ref
// (docs/design/sync.md). This file is the exported API the acceptance
// simulator (acceptance/sim) and the CLI use. The snapshot's encoding is in
// snapshot.go, Load in load.go, Merge in merge.go and Store, which writes a
// merged snapshot back, in store.go; Run, the whole sync, in run.go, over the
// transport in gitref.
package sync

import "time"

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
// and both had a say (acceptance/README.md, "Sync output"). KeptAt and
// DroppedAt are the times of the events that decided the unit on each side;
// the zero time where that side had no event.
type Resolution struct {
	Issue, Field      string
	Kept, Dropped     any
	Won, Reason       string
	KeptAt, DroppedAt time.Time
}

// Options is everything Run needs: the repository (any directory inside
// it), the database, the remote, and the clock and actor for the commit.
type Options struct {
	Dir, DBPath, Remote string
	DryRun              bool
	Now                 func() time.Time
	Actor               string
	// NoPrompt keeps git from asking for a password on the terminal: for
	// a caller with no terminal, such as the session-start hook.
	NoPrompt bool
}

// Result is what a sync did (acceptance/README.md, "Sync output").
type Result struct {
	Remote          string
	Fetched, Pushed int
	Resolved        []Resolution
	DryRun          bool
}
