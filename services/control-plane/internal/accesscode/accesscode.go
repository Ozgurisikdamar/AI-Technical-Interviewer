// Package accesscode makes and checks the credentials of candidate sign-in (ADR-024).
//
// The application number identifies an interview. It is not a secret and may be shown on screen or
// read aloud. The access code is a second credential of the interview's invitation and is stored
// only as an argon2id hash.
//
// Both use Crockford's base32 alphabet, which leaves out I, L, O and U. The usual misreadings
// therefore map back to the right symbol, and a code cannot spell most words.
package accesscode

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Alphabet is Crockford's base32: 32 symbols, so a random byte masked to 5 bits picks one without bias.
const Alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

const (
	// NumberLength is the length of an application number: 40 bits, unique across interviews.
	NumberLength = 8
	// CodeLength is the length of an access code: 50 bits. Guessing is bounded by the sign-in lock
	// online and by argon2id offline.
	CodeLength = 10
	// maxInput bounds the input Normalize reads, separators included.
	maxInput = 32
)

func random(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	for i, v := range b {
		b[i] = Alphabet[v&31]
	}
	return string(b)
}

// NewNumber returns a random application number in canonical form.
func NewNumber() string { return random(NumberLength) }

// NewCode returns a random access code in canonical form.
func NewCode() string { return random(CodeLength) }

// Normalize turns what a person typed into the canonical form. It upper-cases the input, drops
// spaces and hyphens, and reads O as 0 and I, L, ı and İ as 1. It reports false for anything that
// is not exactly length symbols of the alphabet; U, which the alphabet leaves out, is refused.
func Normalize(input string, length int) (string, bool) {
	if len(input) > maxInput {
		return "", false
	}
	out := make([]byte, 0, length)
	for _, r := range input {
		switch r {
		case ' ', '-', ' ':
			continue
		case 'o', 'O':
			r = '0'
		case 'i', 'I', 'l', 'L', 'ı', 'İ':
			r = '1'
		}
		if r >= 'a' && r <= 'z' {
			r -= 'a' - 'A'
		}
		if r > 0x7f || !strings.ContainsRune(Alphabet, r) || len(out) == length {
			return "", false
		}
		out = append(out, byte(r))
	}
	if len(out) != length {
		return "", false
	}
	return string(out), true
}

// FormatNumber shows a canonical application number as XXXX-XXXX.
func FormatNumber(n string) string {
	if len(n) != NumberLength {
		return n
	}
	return n[:4] + "-" + n[4:]
}

// FormatCode shows a canonical access code as XXXXX-XXXXX.
func FormatCode(c string) string {
	if len(c) != CodeLength {
		return c
	}
	return c[:5] + "-" + c[5:]
}

// Params are argon2id's cost parameters.
type Params struct {
	Memory  uint32 // KiB
	Time    uint32
	Threads uint8
}

// Default is OWASP's recommended minimum for argon2id: 19 MiB, two passes, one lane.
var Default = Params{Memory: 19 * 1024, Time: 2, Threads: 1}

// Cheap keeps tests fast. Never use it for stored codes.
var Cheap = Params{Memory: 64, Time: 1, Threads: 1}

const (
	saltLength = 16
	keyLength  = 32
	// Bounds on parameters read back from storage, so that a tampered row cannot make a
	// verification arbitrarily expensive.
	maxMemory  = 64 * 1024
	maxTime    = 10
	maxThreads = 4
)

var b64 = base64.RawStdEncoding

// Hash returns the PHC string of argon2id over a canonical code, with a fresh random salt.
func Hash(code string, p Params) string {
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	key := argon2.IDKey([]byte(code), salt, p.Time, p.Memory, p.Threads, keyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.Memory, p.Time, p.Threads, b64.EncodeToString(salt), b64.EncodeToString(key))
}

// Verify reports whether code matches a PHC string made by Hash, comparing in constant time. A
// malformed string, or one with parameters out of bounds, never matches.
func Verify(code, phc string) bool {
	p, salt, key, ok := parse(phc)
	if !ok {
		return false
	}
	got := argon2.IDKey([]byte(code), salt, p.Time, p.Memory, p.Threads, uint32(len(key)))
	return subtle.ConstantTimeCompare(got, key) == 1
}

func parse(phc string) (Params, []byte, []byte, bool) {
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return Params{}, nil, nil, false
	}
	var memory, time, threads uint32
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil ||
		fmt.Sprintf("m=%d,t=%d,p=%d", memory, time, threads) != parts[3] {
		return Params{}, nil, nil, false
	}
	if memory < 8 || memory > maxMemory || time < 1 || time > maxTime || threads < 1 || threads > maxThreads {
		return Params{}, nil, nil, false
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil || len(salt) != saltLength {
		return Params{}, nil, nil, false
	}
	key, err := b64.DecodeString(parts[5])
	if err != nil || len(key) != keyLength {
		return Params{}, nil, nil, false
	}
	return Params{Memory: memory, Time: time, Threads: uint8(threads)}, salt, key, true
}
