package ids

import (
	"fmt"
	"sort"
	"strings"
)

// Error codes, as they appear in the CLI's error envelope.
const (
	CodeNotFound  = "not_found"
	CodeAmbiguous = "ambiguous_id"
)

// Candidate is an issue the store offers for a typed ID: any issue whose ID
// contains the typed text, retired ones included.
type Candidate struct {
	ID      string
	Retired bool
}

// NotFoundError says no issue matches the typed ID.
type NotFoundError struct {
	Input string
}

func (e *NotFoundError) Error() string { return fmt.Sprintf("no issue matches %q", e.Input) }

// Code returns the stable error code, "not_found".
func (e *NotFoundError) Code() string { return CodeNotFound }

// AmbiguousError says a partial ID matches more than one issue. Candidates
// are the matching IDs, sorted.
type AmbiguousError struct {
	Input      string
	Candidates []string
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("%q matches %d issues: %s", e.Input, len(e.Candidates), strings.Join(e.Candidates, ", "))
}

// Code returns the stable error code, "ambiguous_id".
func (e *AmbiguousError) Code() string { return CodeAmbiguous }

// Resolve picks the issue a typed ID means. In order:
//
//  1. an issue whose ID is exactly the input, retired or not;
//  2. the issues whose ID starts with the input;
//  3. the issues whose ID contains the input.
//
// Steps 2 and 3 look only at issues that aren't retired, and the first step
// with any match decides: one match is the answer, several are an
// AmbiguousError. No match at all is a NotFoundError. Candidates that don't
// contain the input are ignored, so the store may over-supply.
func Resolve(input string, candidates []Candidate) (string, error) {
	if input == "" {
		return "", &NotFoundError{Input: input}
	}
	var prefix, substring []string
	for _, c := range candidates {
		switch {
		case c.ID == input:
			return c.ID, nil
		case c.Retired:
		case strings.HasPrefix(c.ID, input):
			prefix = append(prefix, c.ID)
		case strings.Contains(c.ID, input):
			substring = append(substring, c.ID)
		}
	}
	for _, matches := range [][]string{prefix, substring} {
		switch len(matches) {
		case 0:
		case 1:
			return matches[0], nil
		default:
			sort.Strings(matches)
			return "", &AmbiguousError{Input: input, Candidates: matches}
		}
	}
	return "", &NotFoundError{Input: input}
}
