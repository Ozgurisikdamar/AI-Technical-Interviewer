package evaluation_test

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/evaluation"
)

// TestQuoteRuleMatchesTheSharedCases runs the cases the review console's highlighter is tested with
// (apps/web/src/lib/staff/highlight.test.ts) through Validate itself, so the two cannot drift apart.
// Each case's segments follow the answer's own, and the first evidence item cites them in the
// case's order, which is not always transcript order: Validate must sort them before joining.
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
	if len(fixture.Cases) < 20 {
		t.Fatalf("only %d cases", len(fixture.Cases))
	}
	for _, c := range fixture.Cases {
		a := answer()
		start := int64(10_000)
		spans := map[string][2]int64{}
		for _, s := range c.Segments {
			a.Segments = append(a.Segments, evaluation.Segment{ID: s.ID, Speaker: "candidate", StartMs: start, EndMs: start + 1_000, Text: s.Text})
			spans[s.ID] = [2]int64{start, start + 1_000}
			start += 1_000
		}
		cited := make([]any, len(c.Cited))
		lo, hi := int64(math.MaxInt64), int64(0)
		for i, id := range c.Cited {
			cited[i] = id
			lo, hi = min(lo, spans[id][0]), max(hi, spans[id][1])
		}
		out := valid()
		item := out["evidence"].([]any)[0].(ev)
		item["transcript_segment_ids"], item["quote"], item["transcript_start_ms"], item["transcript_end_ms"] = cited, c.Quote, lo, hi
		if res := run(t, out, a); res.Valid != c.Found {
			t.Errorf("%s: valid=%v, want %v (problems %v)", c.Name, res.Valid, c.Found, res.Problems)
		}
	}
}
