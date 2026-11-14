package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/plongitudes/tesserae/internal/tracker"
)

// stamp is how plain output writes a time.
func stamp(at time.Time) string { return at.UTC().Format(time.RFC3339) }

// writeDetail writes an issue in full for a person: its list line, its
// fields, its texts, and whatever of its links, comments and history are
// loaded. With the history, the comments are shown in it, where they fall. Empty optional values are left out.
func writeDetail(w io.Writer, issue tracker.Issue) {
	writeLine(w, issue)

	field := func(name, value string) {
		if value != "" {
			fmt.Fprintf(w, "%-10s %s\n", name+":", value)
		}
	}
	deref := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	status := issue.Status
	if reason := deref(issue.StatusReason); reason != "" {
		status += " (" + reason + ")"
	}
	if issue.Blocked {
		status += ", blocked"
	}
	if previous := deref(issue.PreviousStatus); previous != "" {
		status += ", was " + previous
	}
	field("status", status)
	field("type", issue.Type)
	field("priority", fmt.Sprint(issue.Priority))
	field("assignee", deref(issue.Assignee))
	field("owner", deref(issue.Owner))
	field("labels", strings.Join(issue.Labels, ", "))
	field("parent", deref(issue.Parent))
	if issue.DueAt != nil {
		field("due", stamp(*issue.DueAt))
	}
	field("created", stamp(issue.CreatedAt)+" by "+issue.CreatedBy)
	field("updated", stamp(issue.UpdatedAt))
	if issue.RetiredAt != nil {
		field("retired", stamp(*issue.RetiredAt))
	}

	if links := issue.Links; links != nil {
		field("blocked by", strings.Join(links.BlockedBy, ", "))
		field("blocks", strings.Join(links.Blocks, ", "))
		field("children", strings.Join(links.Children, ", "))
		field("related", strings.Join(links.Related, ", "))
		field("discovered from", strings.Join(links.DiscoveredFrom, ", "))
		field("discovered", strings.Join(links.Discovered, ", "))
	}

	section := func(name, body string) {
		if body == "" {
			return
		}
		fmt.Fprintf(w, "\n%s:\n", name)
		for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
			fmt.Fprintf(w, "  %s\n", line)
		}
	}
	section("Description", issue.Description)
	section("Design", issue.Design)
	section("Acceptance criteria", issue.AcceptanceCriteria)
	section("Notes", issue.Notes)

	comment := func(c tracker.Comment, says string) {
		fmt.Fprintf(w, "  %s %s%s:\n", stamp(c.CreatedAt), c.Author, says)
		for _, line := range strings.Split(strings.TrimRight(c.Text, "\n"), "\n") {
			fmt.Fprintf(w, "    %s\n", line)
		}
	}
	// History is nil unless it was asked for, and empty for a new issue.
	if issue.History == nil {
		if len(issue.Comments) > 0 {
			fmt.Fprintln(w, "\nComments:")
			for _, c := range issue.Comments {
				comment(c, "")
			}
		}
		return
	}
	// The log, interleaved with the comments (ADR-0013). Both are oldest
	// first; at the same instant a change comes before a comment, as a
	// retirement comes before the comment that keeps its reason.
	fmt.Fprintln(w, "\nHistory:")
	if len(issue.History) == 0 && len(issue.Comments) == 0 {
		fmt.Fprintln(w, "  no changes since it was created")
	}
	comments := issue.Comments
	for _, e := range issue.History {
		for len(comments) > 0 && comments[0].CreatedAt.Before(e.ChangedAt) {
			comment(comments[0], " commented")
			comments = comments[1:]
		}
		fmt.Fprintf(w, "  %s %s: %s %s → %s\n", stamp(e.ChangedAt), e.ChangedBy, e.Attr, eventValue(e.OldValue), eventValue(e.NewValue))
	}
	for _, c := range comments {
		comment(c, " commented")
	}
}

// eventValue writes one side of an event: none for an absent value, and
// the first 12 characters of a text's content hash.
func eventValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "none"
	case string:
		if isHash(x) {
			return "text " + x[:12]
		}
		return x
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

// isHash reports whether s has the shape of a text's content hash.
func isHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	return strings.Trim(s, "0123456789abcdef") == ""
}
