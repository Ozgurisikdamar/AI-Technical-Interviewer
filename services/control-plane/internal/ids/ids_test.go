package ids

import (
	"regexp"
	"testing"
)

func TestIDFormatMatchesContracts(t *testing.T) {
	// Contract patterns are ^<prefix>_[A-Za-z0-9]{16,}$.
	re := regexp.MustCompile(`^ses_[A-Za-z0-9]{22}$`)
	seen := map[string]bool{}
	for range 10000 {
		id := New(Session)
		if !re.MatchString(id) {
			t.Fatalf("bad id %q", id)
		}
		if seen[id] {
			t.Fatalf("collision %q", id)
		}
		seen[id] = true
	}
}

func TestTokensAndHashes(t *testing.T) {
	a, b := Token(), Token()
	if len(a) != 43 || a == b {
		t.Fatalf("unexpected tokens %q %q", a, b)
	}
	first, second := Hash(a), Hash(a)
	if first == a || len(first) != 64 || first != second {
		t.Fatal("hash must be a stable hex sha256 distinct from the secret")
	}
	if !Equal(first, second) || Equal(first, Hash(b)) {
		t.Fatal("constant-time comparison broken")
	}
}
