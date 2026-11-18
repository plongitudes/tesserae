package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/plongitudes/tesserae/internal/tracker"
)

// The values a flag or argument may take are checked here, before the
// tracker is asked anything, so that a bad value is a command-line error
// (cli.md, Output: usage, not a hint) whichever command it was typed for.
// The tracker keeps its own checks for callers that aren't this CLI.

func checkType(typ string) error {
	if slices.Contains(tracker.IssueTypes, typ) {
		return nil
	}
	return usage("%q is not a type; the types are %s", typ, strings.Join(tracker.IssueTypes, ", "))
}

func checkPriority(p int) error {
	if p >= 0 && p <= 4 {
		return nil
	}
	return usage("priority %d is out of range; priorities run from 0 (most urgent) to 4", p)
}

// checkSettableStatus is for `status`: the code stays invalid_status
// (acceptance/README.md, "Errors"), the shape is a command-line error's.
func checkSettableStatus(status string) error {
	if slices.Contains(tracker.Statuses, status) {
		return nil
	}
	all := strings.Join(tracker.Statuses, ", ")
	if status == "waiting" {
		return usageWithCode(tracker.CodeInvalidStatus,
			"waiting can't be set: it is how a ready issue shows while it is blocked or has open children, so set ready; the statuses are %s", all)
	}
	return usageWithCode(tracker.CodeInvalidStatus, "%q is not a status that can be set; the statuses are %s", status, all)
}

// checkDisplayedStatus is for `list --status`, which also takes waiting.
func checkDisplayedStatus(status string) error {
	if status == "" || slices.Contains(tracker.DisplayedStatuses, status) {
		return nil
	}
	return usageWithCode(tracker.CodeInvalidStatus, "%q is not a status; the statuses are %s", status, strings.Join(tracker.DisplayedStatuses, ", "))
}

// usageWithCode is usage with a stable code other than invalid_argument.
func usageWithCode(code, format string, args ...any) error {
	return &usageError{message: fmt.Sprintf(format, args...), code: code}
}
