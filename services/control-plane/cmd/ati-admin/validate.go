package main

// validate-evaluations: the control plane's evaluation validator, offline (DATA_AND_ML_LIFECYCLE
// section 5). The benchmark harness measures evidence validity with the same code that guards
// production. It reads and writes no database.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/evaluation"
)

type validationSegment struct {
	ID      string `json:"id"`
	Speaker string `json:"speaker"`
	StartMs int64  `json:"start_ms"`
	EndMs   int64  `json:"end_ms"`
	Text    string `json:"text"`
}

type validationInput struct {
	Answer struct {
		SessionID         string              `json:"session_id"`
		ItemID            string              `json:"item_id"`
		TranscriptVersion int                 `json:"transcript_version"`
		Segments          []validationSegment `json:"segments"`
		MissingSegments   bool                `json:"missing_segments"`
		IntegrityFlags    []string            `json:"integrity_flags"`
	} `json:"answer"`
	Rubric json.RawMessage `json:"rubric"`
	Output json.RawMessage `json:"output"`
}

type validationResult struct {
	Valid              bool     `json:"valid"`
	Problems           []string `json:"problems"`
	WeightedScoreCenti *int     `json:"weighted_score_centi"`
	RoutingReasons     []string `json:"routing_reasons"`
}

// validateEvaluations applies the control plane's validator (internal/evaluation) to JSON lines of
// {answer, rubric, output} and writes one result line per input, in order. It reads and writes no
// database: the offline benchmark uses it so that evidence validity is measured by the same code that
// guards production (DATA_AND_ML_LIFECYCLE section 5).
func validateEvaluations(path string, out io.Writer) error {
	f, err := os.Open(path) // an operator-supplied input file
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 8<<20)
	enc := json.NewEncoder(out)
	line := 0
	for sc.Scan() {
		line++
		if len(sc.Bytes()) == 0 {
			continue
		}
		var in validationInput
		if err := json.Unmarshal(sc.Bytes(), &in); err != nil {
			return fmt.Errorf("line %d: %w", line, err)
		}
		rubric, err := evaluation.ParseRubric(in.Rubric)
		if err != nil {
			return fmt.Errorf("line %d: %w", line, err)
		}
		a := evaluation.Answer{SessionID: in.Answer.SessionID, ItemID: in.Answer.ItemID, TranscriptVersion: in.Answer.TranscriptVersion,
			MissingSegments: in.Answer.MissingSegments, IntegrityFlags: in.Answer.IntegrityFlags}
		for _, s := range in.Answer.Segments {
			a.Segments = append(a.Segments, evaluation.Segment{ID: s.ID, Speaker: s.Speaker, StartMs: s.StartMs, EndMs: s.EndMs, Text: s.Text})
		}
		res := evaluation.Validate(in.Output, a, rubric)
		r := validationResult{Valid: res.Valid, Problems: res.Problems, RoutingReasons: res.RoutingReasons}
		if r.Problems == nil {
			r.Problems = []string{}
		}
		if res.Valid {
			c := res.WeightedScoreCenti
			r.WeightedScoreCenti = &c
		}
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return sc.Err()
}
