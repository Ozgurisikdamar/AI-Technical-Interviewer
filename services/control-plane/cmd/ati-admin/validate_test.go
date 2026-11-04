package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/contracts"
)

// One evaluation line for the offline validator: the answer, its rubric and an evaluator output that
// quotes quote as evidence for technical_accuracy (score 50) and scores nothing else.
func validationLine(t *testing.T, quote string) string {
	t.Helper()
	rubric := contracts.MustRead(contracts.RubricsDir + "/rub_demo_postgres_mvcc.v1.json")
	text := "Every update writes a new row version and readers keep their snapshot."
	evidence := []any{map[string]any{"dimension": "technical_accuracy", "transcript_start_ms": 1000, "transcript_end_ms": 4000,
		"transcript_segment_ids": []string{"seg_0123456789abcdefAB"}, "quote": quote, "claim": "c", "assessment": "partially_correct", "reference_ids": []string{"ref_pg_mvcc"}}}
	out := map[string]any{
		"session_id": "ses_0123456789abcdefAB", "question_id": "q1", "evaluation_version": "rub_demo_postgres_mvcc@1",
		"transcript_quality": map[string]any{"score": 1, "language": "en", "ambiguities": []string{}, "missing_segments": false},
		"scores":             map[string]int{"technical_accuracy": 50, "architecture_maturity": 0, "edge_case_awareness": 0, "claim_support": 0},
		"weighted_score":     20, "confidence": map[string]any{"score": 0.5, "calibrated": false, "reason": "r"}, "evidence": evidence,
		"missing_concepts": []string{}, "contradictions": []string{}, "unsupported_claims": []string{},
		"hallucination_risk": map[string]any{"score": 0, "severity": "low", "evidence": []string{}}, "recommendation": "HUMAN_REVIEW", "review_required": true,
	}
	line, err := json.Marshal(map[string]any{
		"answer": map[string]any{"session_id": "ses_0123456789abcdefAB", "item_id": "q1", "transcript_version": 1, "missing_segments": false, "integrity_flags": []string{},
			"segments": []any{map[string]any{"id": "seg_0123456789abcdefAB", "speaker": "candidate", "start_ms": 1000, "end_ms": 4000, "text": text}}},
		"rubric": json.RawMessage(rubric), "output": out,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(line)
}

func TestValidateEvaluationsAppliesTheControlPlaneValidatorOffline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "in.jsonl")
	in := validationLine(t, "a new row version") + "\n\n" + validationLine(t, "VACUUM is never needed") + "\n"
	if err := os.WriteFile(path, []byte(in), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := validateEvaluations(path, &out); err != nil {
		t.Fatal(err)
	}
	var results []validationResult
	sc := bufio.NewScanner(&out)
	for sc.Scan() {
		var r validationResult
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		results = append(results, r)
	}
	if len(results) != 2 {
		t.Fatalf("one result per input line, blank lines skipped: %d", len(results))
	}
	if !results[0].Valid || results[0].WeightedScoreCenti == nil || *results[0].WeightedScoreCenti != 2000 || len(results[0].Problems) != 0 {
		t.Fatalf("valid output: %+v", results[0])
	}
	if results[1].Valid || results[1].WeightedScoreCenti != nil || !strings.Contains(strings.Join(results[1].Problems, ";"), "do not appear") {
		t.Fatalf("a fabricated quote is rejected: %+v", results[1])
	}
}

func TestValidateEvaluationsRefusesMalformedInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "in.jsonl")
	if err := os.WriteFile(path, []byte("not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateEvaluations(path, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("malformed input: %v", err)
	}
	if err := validateEvaluations(filepath.Join(t.TempDir(), "missing.jsonl"), &bytes.Buffer{}); err == nil {
		t.Fatal("a missing input file is an error")
	}
}
