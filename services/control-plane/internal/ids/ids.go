// Package ids generates opaque identifiers and secrets. Secrets are only ever stored as hashes.
package ids

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"math/big"
)

const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// Prefixes used across the system. Identifiers never contain personal data.
const (
	Tenant     = "ten"
	Session    = "ses"
	Activation = "act"
	Segment    = "seg"
	Answer     = "ans"
	Event      = "evt"
	Lease      = "lea"
	Recording  = "rec"
	Invitation = "inv"
	Staff      = "stf"
	Room       = "rm"
	Request    = "req"
	Consent    = "cns"
	Candidate  = "cand"
	// Phase 2 (ADR-022).
	EvaluationAttempt = "eva"
	Evaluation        = "evl"
	ReviewTask        = "rvt"
	ReviewOutcome     = "rvo"
	Report            = "rep"
)

// New returns prefix_<22 base62 characters> (about 131 bits of randomness).
func New(prefix string) string {
	b := make([]byte, 22)
	max := big.NewInt(int64(len(base62)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic("crypto/rand unavailable: " + err.Error())
		}
		b[i] = base62[n.Int64()]
	}
	return prefix + "_" + string(b)
}

// Token returns a 256-bit URL-safe secret.
func Token() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// Hash returns the hex SHA-256 of a secret. Only hashes are persisted.
func Hash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// Equal compares two strings in constant time.
func Equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
