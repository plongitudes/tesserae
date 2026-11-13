package ids

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
)

var testSeed = Seed{
	Prefix:    "tess",
	Title:     "Fix the flux capacitor",
	Creator:   "tester",
	CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
}

func TestGenerateHasThePrefixAndABase36HashOfTheAskedLength(t *testing.T) {
	for length := MinLength; length <= MaxLength; length++ {
		id := Generate(testSeed, 0, length)
		want := regexp.MustCompile(`^tess-[0-9a-z]{` + string(rune('0'+length)) + `}$`)
		if !want.MatchString(id) {
			t.Errorf("Generate(length %d) = %q; want tess-<%d base36 chars>", length, id, length)
		}
	}
}

func TestGenerateKeepsAPrefixThatContainsHyphens(t *testing.T) {
	seed := testSeed
	seed.Prefix = "my-repo"
	id := Generate(seed, 0, 4)
	if !regexp.MustCompile(`^my-repo-[0-9a-z]{4}$`).MatchString(id) {
		t.Errorf("Generate = %q; want my-repo-<4 base36 chars>", id)
	}
}

func TestGenerateIsDeterministic(t *testing.T) {
	if a, b := Generate(testSeed, 0, 6), Generate(testSeed, 0, 6); a != b {
		t.Errorf("same seed gave %q and %q", a, b)
	}
}

func TestGenerateDependsOnEveryPartOfTheSeedAndTheNonce(t *testing.T) {
	base := Generate(testSeed, 0, 8)
	variants := map[string]string{}

	s := testSeed
	s.Prefix = "other"
	variants["prefix"] = strings.TrimPrefix(Generate(s, 0, 8), "other-")
	s = testSeed
	s.Title = "Another title"
	variants["title"] = strings.TrimPrefix(Generate(s, 0, 8), "tess-")
	s = testSeed
	s.Creator = "someone else"
	variants["creator"] = strings.TrimPrefix(Generate(s, 0, 8), "tess-")
	s = testSeed
	s.CreatedAt = s.CreatedAt.Add(time.Nanosecond)
	variants["created_at"] = strings.TrimPrefix(Generate(s, 0, 8), "tess-")
	variants["nonce"] = strings.TrimPrefix(Generate(testSeed, 1, 8), "tess-")

	for part, hash := range variants {
		if hash == strings.TrimPrefix(base, "tess-") {
			t.Errorf("changing the %s left the hash at %q", part, hash)
		}
	}
}

func TestGenerateDoesNotConfuseAdjacentFields(t *testing.T) {
	a := Seed{Prefix: "p", Title: "ab", Creator: "c", CreatedAt: testSeed.CreatedAt}
	b := Seed{Prefix: "p", Title: "a", Creator: "bc", CreatedAt: testSeed.CreatedAt}
	if Generate(a, 0, 8) == Generate(b, 0, 8) {
		t.Errorf("title/creator boundary is not part of the hash")
	}
}

func TestGenerateSeesTheSameInstantInAnyTimeZone(t *testing.T) {
	s := testSeed
	s.CreatedAt = s.CreatedAt.In(time.FixedZone("x", 5*3600))
	if Generate(s, 0, 6) != Generate(testSeed, 0, 6) {
		t.Errorf("the same instant in another zone gave a different ID")
	}
}

// The thresholds are where the chance of any collision among n random
// base36 IDs passes 25% (birthday bound): n ≈ sqrt(2·36^len·ln(4/3)).
func TestLengthGrowsWithTheIssueCount(t *testing.T) {
	cases := []struct{ count, want int }{
		{0, 3}, {1, 3}, {100, 3}, {163, 3},
		{164, 4}, {500, 4}, {983, 4},
		{984, 5}, {5000, 5}, {5898, 5},
		{5899, 6}, {35000, 6},
		{36000, 7}, {200000, 7},
		{250000, 8}, {10000000, 8},
	}
	for _, c := range cases {
		if got := Length(c.count); got != c.want {
			t.Errorf("Length(%d) = %d; want %d", c.count, got, c.want)
		}
	}
}

func TestAllocateReturnsTheFirstCandidateWhenNothingIsTaken(t *testing.T) {
	id, err := Allocate(testSeed, 0, func(string) (bool, error) { return false, nil })
	if err != nil {
		t.Fatal(err)
	}
	if want := Generate(testSeed, 0, 3); id != want {
		t.Errorf("Allocate = %q; want %q", id, want)
	}
}

func TestAllocateUsesTheAdaptiveLength(t *testing.T) {
	id, err := Allocate(testSeed, 1000, func(string) (bool, error) { return false, nil })
	if err != nil {
		t.Fatal(err)
	}
	if got := len(strings.TrimPrefix(id, "tess-")); got != 5 {
		t.Errorf("hash length with 1000 issues = %d; want 5", got)
	}
}

func TestAllocateTriesAnotherNonceOnCollision(t *testing.T) {
	first := Generate(testSeed, 0, 3)
	id, err := Allocate(testSeed, 0, func(id string) (bool, error) { return id == first, nil })
	if err != nil {
		t.Fatal(err)
	}
	if id == first {
		t.Fatalf("Allocate returned the taken ID %q", id)
	}
	if !regexp.MustCompile(`^tess-[0-9a-z]{3}$`).MatchString(id) {
		t.Errorf("Allocate = %q; want another 3-character ID", id)
	}
}

func TestAllocateGrowsTheIDWhenEveryNonceCollides(t *testing.T) {
	// Everything of length 3 is taken.
	id, err := Allocate(testSeed, 0, func(id string) (bool, error) {
		return len(id) == len("tess-")+3, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^tess-[0-9a-z]{4}$`).MatchString(id) {
		t.Errorf("Allocate = %q; want a 4-character ID", id)
	}
}

func TestAllocateNeverReturnsATakenID(t *testing.T) {
	taken := map[string]bool{}
	for i := 0; i < 500; i++ {
		seed := testSeed
		seed.CreatedAt = seed.CreatedAt.Add(time.Duration(i))
		id, err := Allocate(seed, len(taken), func(id string) (bool, error) { return taken[id], nil })
		if err != nil {
			t.Fatal(err)
		}
		if taken[id] {
			t.Fatalf("issue %d got the taken ID %q", i, id)
		}
		taken[id] = true
	}
}

func TestAllocateGivesUpWhenEverythingIsTaken(t *testing.T) {
	if _, err := Allocate(testSeed, 0, func(string) (bool, error) { return true, nil }); err == nil {
		t.Fatal("Allocate found an ID with every candidate taken")
	}
}

func TestAllocatePassesOnALookupError(t *testing.T) {
	boom := errors.New("boom")
	if _, err := Allocate(testSeed, 0, func(string) (bool, error) { return false, boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v; want the lookup error", err)
	}
}
