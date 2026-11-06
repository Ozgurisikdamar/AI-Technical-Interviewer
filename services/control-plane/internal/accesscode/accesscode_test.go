package accesscode_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/accesscode"
)

type sharedCase struct {
	Name      string  `json:"name"`
	Kind      string  `json:"kind"`
	Input     string  `json:"input"`
	Canonical *string `json:"canonical"`
}

// The cases the web app runs too (contracts/fixtures/sign-in-codes), so both read input alike.
func TestNormalizeAgreesWithTheSharedCases(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "contracts", "fixtures", "sign-in-codes", "cases.v1.json"))
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal(err)
		}
		t.Skipf("shared cases not found: %v", err)
	}
	var fixture struct {
		Cases []sharedCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) < 20 {
		t.Fatalf("only %d shared cases", len(fixture.Cases))
	}
	for _, c := range fixture.Cases {
		length := map[string]int{"number": accesscode.NumberLength, "code": accesscode.CodeLength}[c.Kind]
		if length == 0 {
			t.Fatalf("%s: unknown kind %q", c.Name, c.Kind)
		}
		got, ok := accesscode.Normalize(c.Input, length)
		switch {
		case c.Canonical == nil && ok:
			t.Errorf("%s: %q was accepted as %q", c.Name, c.Input, got)
		case c.Canonical != nil && (!ok || got != *c.Canonical):
			t.Errorf("%s: %q gave %q (%v), want %q", c.Name, c.Input, got, ok, *c.Canonical)
		}
	}
}

func TestNewValuesAreCanonicalAndFormatted(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		n, c := accesscode.NewNumber(), accesscode.NewCode()
		for _, v := range []struct {
			value  string
			length int
		}{{n, accesscode.NumberLength}, {c, accesscode.CodeLength}} {
			if got, ok := accesscode.Normalize(v.value, v.length); !ok || got != v.value {
				t.Fatalf("%q is not canonical", v.value)
			}
		}
		seen[c] = true
		if f := accesscode.FormatNumber(n); len(f) != 9 || f[4] != '-' {
			t.Fatalf("number shown as %q", f)
		}
		if f := accesscode.FormatCode(c); len(f) != 11 || f[5] != '-' {
			t.Fatalf("code shown as %q", f)
		}
		if got, _ := accesscode.Normalize(accesscode.FormatCode(c), accesscode.CodeLength); got != c {
			t.Fatalf("the shown code does not read back: %q", c)
		}
	}
	if len(seen) < 200 {
		t.Fatalf("codes repeat: %d distinct of 200", len(seen))
	}
}

func TestHashVerifiesOnlyTheRightCode(t *testing.T) {
	code := accesscode.NewCode()
	phc := accesscode.Hash(code, accesscode.Cheap)
	if strings.Contains(phc, code) {
		t.Fatal("the hash contains the code")
	}
	if !accesscode.Verify(code, phc) {
		t.Fatal("the right code was refused")
	}
	if accesscode.Verify(accesscode.NewCode(), phc) || accesscode.Verify("", phc) {
		t.Fatal("a wrong code was accepted")
	}
	if accesscode.Hash(code, accesscode.Cheap) == phc {
		t.Fatal("two hashes of one code share a salt")
	}
}

func TestStoredCodesUseTheOWASPParameters(t *testing.T) {
	phc := accesscode.Hash(accesscode.NewCode(), accesscode.Default)
	if !strings.HasPrefix(phc, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("default parameters: %s", phc)
	}
}

func TestMalformedOrCostlyHashesNeverMatch(t *testing.T) {
	code := accesscode.NewCode()
	good := accesscode.Hash(code, accesscode.Cheap)
	parts := strings.Split(good, "$")
	with := func(i int, v string) string {
		p := append([]string{}, parts...)
		p[i] = v
		return strings.Join(p, "$")
	}
	for name, phc := range map[string]string{
		"empty":             "",
		"argon2i":           with(1, "argon2i"),
		"old version":       with(2, "v=16"),
		"too much memory":   with(3, "m=1048576,t=1,p=1"),
		"too many passes":   with(3, "m=64,t=99,p=1"),
		"too many lanes":    with(3, "m=64,t=1,p=9"),
		"trailing garbage":  with(3, "m=64,t=1,p=1x"),
		"short salt":        with(4, parts[4][:10]),
		"short key":         with(5, parts[5][:20]),
		"not base64":        with(5, strings.Repeat("!", 43)),
		"extra field":       good + "$x",
		"code in the clear": code,
	} {
		if accesscode.Verify(code, phc) {
			t.Errorf("%s: matched", name)
		}
	}
}
