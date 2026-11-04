package evaluation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// TestQuoteRuleMatchesTheSharedCases runs the verbatim quote rule against the cases the review
// console's highlighter is tested with (apps/web/src/lib/staff/highlight.test.ts), so the two
// cannot drift apart.
func TestQuoteRuleMatchesTheSharedCases(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "contracts", "fixtures", "quote-matching", "cases.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name     string `json:"name"`
			Segments []struct {
				ID   string `json:"id"`
				Text string `json:"text"`
			} `json:"segments"`
			Cited []string `json:"cited"`
			Quote string   `json:"quote"`
			Found bool     `json:"found"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) < 10 {
		t.Fatalf("only %d cases", len(fixture.Cases))
	}
	for _, c := range fixture.Cases {
		var texts []string // the cited segments, in transcript order, as Validate passes them
		for _, s := range c.Segments {
			if slices.Contains(c.Cited, s.ID) {
				texts = append(texts, s.Text)
			}
		}
		if got := quoteFound(texts, c.Quote); got != c.Found {
			t.Errorf("%s: found=%v, want %v", c.Name, got, c.Found)
		}
	}
}
