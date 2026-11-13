// Package ids holds the rules for issue IDs: how a new one is generated and
// how a partial ID typed by a user resolves to an issue (ADR-0017). It is
// pure: the store supplies the facts (how many issues exist, whether an ID is
// taken, which IDs contain some text) and this package decides.
package ids

import (
	"crypto/sha256"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"
)

const (
	// MinLength and MaxLength bound the base36 hash part of a generated ID.
	MinLength = 3
	MaxLength = 8

	// A new ID's length is the shortest at which the chance that any two of
	// the existing issues' IDs collide stays at or below this.
	maxCollisionProbability = 0.25

	// How many nonces Allocate tries at one length before growing the ID.
	noncesPerLength = 10
)

// Seed is what a new issue's ID is derived from.
type Seed struct {
	Prefix    string
	Title     string
	Creator   string
	CreatedAt time.Time
}

// Generate returns "<prefix>-<hash>", the hash being length lowercase base36
// characters of a SHA-256 over the seed and the nonce. length is clamped to
// MinLength..MaxLength. The same arguments always give the same ID.
func Generate(s Seed, nonce, length int) string {
	length = min(max(length, MinLength), MaxLength)

	// Each field is length-prefixed so that no two seeds share an encoding.
	h := sha256.New()
	for _, field := range []string{s.Prefix, s.Title, s.Creator} {
		fmt.Fprintf(h, "%d:%s|", len(field), field)
	}
	fmt.Fprintf(h, "%d|%d", s.CreatedAt.UnixNano(), nonce)

	// The hash as a number, reduced to exactly length base36 digits.
	n := new(big.Int).SetBytes(h.Sum(nil))
	space := new(big.Int).Exp(big.NewInt(36), big.NewInt(int64(length)), nil)
	digits := n.Mod(n, space).Text(36)
	return s.Prefix + "-" + strings.Repeat("0", length-len(digits)) + digits
}

// Length returns the hash length for a new ID when issueCount issues already
// exist: the shortest length that keeps the birthday-bound collision
// probability, 1 - e^(-n²/2N) over N = 36^length IDs, within the threshold.
func Length(issueCount int) int {
	n := float64(issueCount)
	for length := MinLength; length < MaxLength; length++ {
		p := 1 - math.Exp(-n*n/(2*math.Pow(36, float64(length))))
		if p <= maxCollisionProbability {
			return length
		}
	}
	return MaxLength
}

// Allocate returns an unused ID for a new issue. It starts at the adaptive
// length for issueCount and asks taken about each candidate, trying further
// nonces on a collision and then longer IDs. For the result to stay unused,
// call it inside the transaction that inserts the issue, with taken reading
// through that transaction.
func Allocate(s Seed, issueCount int, taken func(id string) (bool, error)) (string, error) {
	for length := Length(issueCount); length <= MaxLength; length++ {
		for nonce := 0; nonce < noncesPerLength; nonce++ {
			id := Generate(s, nonce, length)
			used, err := taken(id)
			if err != nil {
				return "", fmt.Errorf("check whether ID %s is taken: %w", id, err)
			}
			if !used {
				return id, nil
			}
		}
	}
	return "", fmt.Errorf("no unused ID found for a new %s issue after %d nonces at every length up to %d",
		s.Prefix, noncesPerLength, MaxLength)
}
