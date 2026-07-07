// Package agentid is the Go port of the canonical o2r agent-id generator: a
// short id for naming agents (o2r channels, container tags, dozzle rows) built
// as two lowercase letters then two digits (`ab54`, `cd97`). The alphabet drops
// the visually and phonetically ambiguous characters - the dictatable set first
// used by the archived o2r channel protocol and documented in aos'
// docs/dictatable-id-alphabet.md.
//
// aos owns the canonical definition (agentic_os/agent_id.py); this package is
// the verified Go port ward and cli-guard mint ids with. The alphabet, the
// two-letters-then-two-digits shape, and the seeded variant are a cross-language
// contract, pinned byte-for-byte by the vendored agent_id_vectors.json and
// asserted in the drift test. A change here that diverges from that vector fails
// CI, which is the point.
//
// Seed algorithm (the portable parity contract): digest = sha256(utf-8(seed)),
// then id = Letters[digest[0]%22] + Letters[digest[1]%22] + Digits[digest[2]%6]
// + Digits[digest[3]%6]. The seeded form is a test anchor only; real ids come
// from NewID, which is crypto/rand-backed and uniform, not reproducible.
package agentid

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"math/big"
	"strings"
)

// Alphabet ground truth, lowercased: the confusable/homophone characters
// (i l n o, 0 1 2 3) are dropped. Rationale in aos docs/dictatable-id-alphabet.md.
const (
	// Letters is the 22-letter dictatable set the id's first two chars draw from.
	Letters = "abcdefghjkmpqrstuvwxyz"
	// Digits is the 6-digit dictatable set the id's last two chars draw from.
	Digits = "456789"
	// Alphabet is the full dictatable alphabet (letters then digits).
	Alphabet = Letters + Digits
	// Len is the total id length.
	Len = 4
	// LetterLen is how many leading chars are letters (the rest are digits).
	LetterLen = 2
)

// NewID returns a fresh crypto/rand-backed id, uniform over the alphabet: 2
// dictatable letters then 2 digits. Not reproducible - use SeededID for parity.
func NewID() (string, error) {
	var b strings.Builder
	b.Grow(Len)
	for i := 0; i < Len; i++ {
		set := Letters
		if i >= LetterLen {
			set = Digits
		}
		c, err := choose(set)
		if err != nil {
			return "", err
		}
		b.WriteByte(c)
	}
	return b.String(), nil
}

// choose returns a uniformly random byte from set using crypto/rand. big.Int's
// rand.Int does the rejection sampling, so there is no modulo bias.
func choose(set string) (byte, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(set))))
	if err != nil {
		return 0, fmt.Errorf("agentid: random source failed: %w", err)
	}
	return set[n.Int64()], nil
}

// IsValid reports whether raw is already a canonical id (lowercase, correct
// shape: LetterLen letters then the balance in digits).
func IsValid(raw string) bool {
	if len(raw) != Len {
		return false
	}
	for i := 0; i < Len; i++ {
		set := Letters
		if i >= LetterLen {
			set = Digits
		}
		if strings.IndexByte(set, raw[i]) < 0 {
			return false
		}
	}
	return true
}

// Normalize canonicalizes a spoken/typed id (trim + lowercase) into the
// lowercase stored form, or returns an error when the result is not a valid id.
func Normalize(raw string) (string, error) {
	cid := strings.ToLower(strings.TrimSpace(raw))
	if !IsValid(cid) {
		return "", fmt.Errorf("agentid: not a canonical agent id: %q", raw)
	}
	return cid, nil
}

// SeededID is the deterministic seed->id parity anchor (algorithm in the package
// doc). Not for real ids - use NewID; this only reproduces agent_id_vectors.json.
func SeededID(seed string) string {
	digest := sha256.Sum256([]byte(seed))
	return string([]byte{
		Letters[int(digest[0])%len(Letters)],
		Letters[int(digest[1])%len(Letters)],
		Digits[int(digest[2])%len(Digits)],
		Digits[int(digest[3])%len(Digits)],
	})
}
