package ids

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func live(ids ...string) []Candidate {
	var cs []Candidate
	for _, id := range ids {
		cs = append(cs, Candidate{ID: id})
	}
	return cs
}

func retired(id string) Candidate { return Candidate{ID: id, Retired: true} }

func wantResolved(t *testing.T, input string, cs []Candidate, want string) {
	t.Helper()
	got, err := Resolve(input, cs)
	if err != nil || got != want {
		t.Fatalf("Resolve(%q) = %q, %v; want %q", input, got, err, want)
	}
}

func wantNotFound(t *testing.T, input string, cs []Candidate) {
	t.Helper()
	got, err := Resolve(input, cs)
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("Resolve(%q) = %q, %v; want a NotFoundError", input, got, err)
	}
	if nf.Code() != "not_found" || nf.Input != input {
		t.Errorf("NotFoundError = code %q, input %q; want not_found, %q", nf.Code(), nf.Input, input)
	}
	if !strings.Contains(nf.Error(), input) {
		t.Errorf("message %q does not name the input", nf.Error())
	}
}

func wantAmbiguous(t *testing.T, input string, cs []Candidate, want ...string) {
	t.Helper()
	got, err := Resolve(input, cs)
	var amb *AmbiguousError
	if !errors.As(err, &amb) {
		t.Fatalf("Resolve(%q) = %q, %v; want an AmbiguousError", input, got, err)
	}
	if amb.Code() != "ambiguous_id" {
		t.Errorf("Code() = %q; want ambiguous_id", amb.Code())
	}
	if !reflect.DeepEqual(amb.Candidates, want) {
		t.Errorf("Candidates = %v; want %v", amb.Candidates, want)
	}
	for _, id := range want {
		if !strings.Contains(amb.Error(), id) {
			t.Errorf("message %q does not list %s", amb.Error(), id)
		}
	}
}

func TestResolveExactID(t *testing.T) {
	wantResolved(t, "t-abc", live("t-abc"), "t-abc")
}

func TestResolveExactWinsOverLongerIDsThatStartWithIt(t *testing.T) {
	wantResolved(t, "t-abc", live("t-abcd", "t-abc", "t-abce"), "t-abc")
}

func TestResolveUniquePrefix(t *testing.T) {
	wantResolved(t, "t-ab", live("t-abc", "t-xyz"), "t-abc")
}

func TestResolvePrefixWinsOverSubstring(t *testing.T) {
	// "t-a" starts t-abc and merely occurs inside but-ab.
	wantResolved(t, "t-a", live("but-ab", "t-abc"), "t-abc")
}

func TestResolveSeveralPrefixMatchesAreAmbiguous(t *testing.T) {
	wantAmbiguous(t, "t-", live("t-xyz", "t-abc", "u-t-9"), "t-abc", "t-xyz")
}

func TestResolveUniqueSubstring(t *testing.T) {
	wantResolved(t, "bc1", live("t-abc1", "t-xyz2"), "t-abc1")
}

func TestResolveBareHash(t *testing.T) {
	wantResolved(t, "abc", live("t-abc", "t-xyz"), "t-abc")
}

func TestResolveSeveralSubstringMatchesAreAmbiguous(t *testing.T) {
	wantAmbiguous(t, "abc", live("t-abcd", "t-xyz", "t-abc"), "t-abc", "t-abcd")
}

func TestResolveNothingMatches(t *testing.T) {
	wantNotFound(t, "t-zzzzzzzzzz", live("t-abc", "t-xyz"))
	wantNotFound(t, "t-abc", nil)
}

func TestResolveEmptyInputMatchesNothing(t *testing.T) {
	wantNotFound(t, "", live("t-abc"))
}

func TestResolveRetiredIssueByExactID(t *testing.T) {
	wantResolved(t, "t-abc", []Candidate{retired("t-abc")}, "t-abc")
}

func TestResolveRetiredIssuesNeverMatchAPartialID(t *testing.T) {
	cs := []Candidate{retired("t-abc")}
	wantNotFound(t, "t-ab", cs)
	wantNotFound(t, "abc", cs)
	wantNotFound(t, "b", cs)
}

func TestResolveRetiredIssuesDoNotMakeAPartialIDAmbiguous(t *testing.T) {
	cs := []Candidate{retired("t-abc"), {ID: "t-abd"}}
	wantResolved(t, "t-ab", cs, "t-abd")
	wantResolved(t, "ab", cs, "t-abd")
}

func TestResolveImportedDottedIDs(t *testing.T) {
	cs := live("bd-abc", "bd-abc.1", "bd-abc.10", "t-xyz")
	wantResolved(t, "bd-abc", cs, "bd-abc")
	wantResolved(t, "bd-abc.1", cs, "bd-abc.1")
	wantResolved(t, "bd-abc.10", cs, "bd-abc.10")
	wantAmbiguous(t, "abc.1", cs, "bd-abc.1", "bd-abc.10")
}

func TestResolveIgnoresCandidatesThatDoNotContainTheInput(t *testing.T) {
	// The store may hand over more than it needs to.
	wantResolved(t, "xyz", live("t-abc", "t-xyz"), "t-xyz")
}
