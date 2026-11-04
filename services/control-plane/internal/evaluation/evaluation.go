// Package evaluation checks Tier-2 evaluator output in trusted code (ADR-007, ADR-013, ADR-022).
//
// An evaluator's JSON is a claim to verify, never a result to trust. The schema, the identity of
// the answer, the scores and their weighted sum, and every piece of cited evidence are checked
// against the stored transcript before anything is kept as an evaluation. The package is a pure
// function of its inputs: it reads nothing and writes nothing (internal/archtest).
package evaluation

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/unicode/norm"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/contracts"
	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/contracts/schema"
)

// Dimensions are the scored dimensions of the evaluation contract, in contract order.
var Dimensions = []string{"technical_accuracy", "architecture_maturity", "edge_case_awareness", "claim_support"}

// DocumentedWeights are the dimension weights of BLUEPRINT section 7 and PROMPT_AND_EVALUATION
// section 4, in hundredths. A rubric must use exactly these.
var DocumentedWeights = map[string]int{"technical_accuracy": 40, "architecture_maturity": 25, "edge_case_awareness": 20, "claim_support": 15}

// Routing policy thresholds. They are uncalibrated policy values, not probabilities
// (PROMPT_AND_EVALUATION section 6): they decide which reasons a reviewer is shown.
const (
	ReviewThreshold            = 0.85 // evaluator confidence below this is flagged
	TranscriptQualityThreshold = 0.6  // evaluator-reported transcript quality below this is flagged
	DisagreementPoints         = 20   // two evaluators differing by more than this on a dimension
)

// Rubric is what validation needs from a stored rubric (rubric contract v1).
type Rubric struct {
	ID           string
	Version      int
	Weights      map[string]int // hundredths, equal to DocumentedWeights
	ReferenceIDs map[string]bool
}

// Ref is the rubric reference an evaluation must echo as evaluation_version (conflict C4: the
// other versions live in the database record, not in the evaluator's output).
func (r *Rubric) Ref() string { return fmt.Sprintf("%s@%d", r.ID, r.Version) }

// ParseRubric reads a stored rubric and checks that it scores with the documented weights.
func ParseRubric(raw []byte) (*Rubric, error) {
	if err := schema.ValidateJSON(contracts.RubricPath, raw); err != nil {
		return nil, fmt.Errorf("rubric does not match the rubric contract: %w", err)
	}
	var body struct {
		RubricID   string             `json:"rubric_id"`
		Version    int                `json:"version"`
		Weights    map[string]float64 `json:"weights"`
		References []struct {
			ReferenceID string `json:"reference_id"`
		} `json:"references"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	r := &Rubric{ID: body.RubricID, Version: body.Version, Weights: map[string]int{}, ReferenceIDs: map[string]bool{}}
	for _, d := range Dimensions {
		w := body.Weights[d] * 100
		if math.Abs(w-math.Round(w)) > 1e-9 || int(math.Round(w)) != DocumentedWeights[d] {
			return nil, fmt.Errorf("rubric %s weight for %s is %v; the documented weight is %d%%", r.Ref(), d, body.Weights[d], DocumentedWeights[d])
		}
		r.Weights[d] = DocumentedWeights[d]
	}
	for _, ref := range body.References {
		r.ReferenceIDs[ref.ReferenceID] = true
	}
	return r, nil
}

// Segment is a stored transcript segment.
type Segment struct {
	ID      string
	Speaker string
	StartMs int64
	EndMs   int64
	Text    string
}

// Answer is what was evaluated: the answer's evidence-eligible candidate segments of one
// transcript version, in transcript order, and the flags recorded when it was finalized.
type Answer struct {
	SessionID         string
	ItemID            string
	TranscriptVersion int
	Segments          []Segment
	MissingSegments   bool
	IntegrityFlags    []string
}

// EvidenceItem is one evidence entry of the evaluation contract.
type EvidenceItem struct {
	Dimension    string   `json:"dimension"`
	StartMs      int64    `json:"transcript_start_ms"`
	EndMs        int64    `json:"transcript_end_ms"`
	SegmentIDs   []string `json:"transcript_segment_ids"`
	Quote        string   `json:"quote"`
	Claim        string   `json:"claim"`
	Assessment   string   `json:"assessment"`
	ReferenceIDs []string `json:"reference_ids"`
}

// Result is the outcome of validation. Problems never contain candidate text: they are stored
// and may be logged.
type Result struct {
	Valid              bool
	Problems           []string
	Scores             map[string]int
	WeightedScoreCenti int // hundredths of a point, recomputed here
	Confidence         float64
	Recommendation     string // advisory (conflict C5)
	OutputRisk         string // the evaluator output's own risk, never the candidate's (conflict C3)
	Evidence           []EvidenceItem
	RoutingReasons     []string
}

type output struct {
	SessionID         string `json:"session_id"`
	QuestionID        string `json:"question_id"`
	EvaluationVersion string `json:"evaluation_version"`
	TranscriptQuality struct {
		Score           float64 `json:"score"`
		MissingSegments bool    `json:"missing_segments"`
	} `json:"transcript_quality"`
	Scores        map[string]json.Number `json:"scores"`
	WeightedScore float64                `json:"weighted_score"`
	Confidence    struct {
		Score      float64 `json:"score"`
		Calibrated bool    `json:"calibrated"`
	} `json:"confidence"`
	Evidence          []EvidenceItem `json:"evidence"`
	Contradictions    []string       `json:"contradictions"`
	UnsupportedClaims []string       `json:"unsupported_claims"`
	HallucinationRisk struct {
		Severity string `json:"severity"`
	} `json:"hallucination_risk"`
	Recommendation string `json:"recommendation"`
	ReviewRequired bool   `json:"review_required"`
}

var supporting = map[string]bool{"correct": true, "partially_correct": true}

// Validate checks raw evaluator output for an answer against its rubric.
func Validate(raw []byte, a Answer, r *Rubric) *Result {
	res := &Result{}
	fail := func(format string, args ...any) { res.Problems = append(res.Problems, fmt.Sprintf(format, args...)) }
	if len(a.Segments) == 0 {
		fail("the answer has no evidence-eligible candidate speech; the evaluator must abstain")
		return res
	}
	if err := schema.ValidateJSON(contracts.EvaluationPath, raw); err != nil {
		res.Problems = append(res.Problems, schemaProblems(err)...)
		return res
	}
	var out output
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		fail("output is not a single JSON object")
		return res
	}
	if out.SessionID != a.SessionID {
		fail("session_id does not match the answer's session")
	}
	if out.QuestionID != a.ItemID {
		fail("question_id does not match the answer's plan item")
	}
	if out.EvaluationVersion != r.Ref() {
		fail("evaluation_version is not the rubric reference %s", r.Ref())
	}
	res.Scores = map[string]int{}
	for _, d := range Dimensions {
		n, err := strconv.Atoi(string(out.Scores[d]))
		if err != nil {
			fail("score %s is not an integer", d)
			continue
		}
		res.Scores[d] = n
		res.WeightedScoreCenti += n * r.Weights[d]
	}
	if len(res.Scores) == len(Dimensions) && int(math.Round(out.WeightedScore*100)) != res.WeightedScoreCenti {
		fail("weighted_score does not equal the weighted sum of the scores (%.2f)", float64(res.WeightedScoreCenti)/100)
	}
	if out.Confidence.Calibrated {
		fail("confidence claims a calibration that does not exist")
	}
	res.Confidence = out.Confidence.Score

	segments := map[string]Segment{}
	order := map[string]int{}
	for i, s := range a.Segments {
		segments[s.ID], order[s.ID] = s, i
	}
	supported := map[string]bool{}
	for i, e := range out.Evidence {
		label := fmt.Sprintf("evidence %d (%s)", i+1, e.Dimension)
		cited := make([]Segment, 0, len(e.SegmentIDs))
		for _, id := range e.SegmentIDs {
			s, ok := segments[id]
			switch {
			case !ok:
				fail("%s cites a segment that is not part of this answer", label)
			case s.Speaker != "candidate":
				fail("%s cites interviewer speech", label)
			default:
				cited = append(cited, s)
			}
		}
		if len(cited) != len(e.SegmentIDs) {
			continue
		}
		slices.SortFunc(cited, func(x, y Segment) int { return order[x.ID] - order[y.ID] })
		lo, hi := cited[0].StartMs, cited[0].EndMs
		texts := make([]string, 0, len(cited))
		for _, s := range cited {
			lo, hi = min(lo, s.StartMs), max(hi, s.EndMs)
			texts = append(texts, s.Text)
		}
		if e.StartMs > e.EndMs || e.StartMs < lo || e.EndMs > hi {
			fail("%s has a time span outside the cited segments", label)
		}
		if e.Assessment != "not_addressed" && !quoteFound(texts, e.Quote) {
			fail("%s quotes words that do not appear in the cited segments", label)
		}
		for _, ref := range e.ReferenceIDs {
			if !r.ReferenceIDs[ref] {
				fail("%s cites a reference that is not part of the rubric", label)
			}
		}
		if supporting[e.Assessment] {
			supported[e.Dimension] = true
		}
	}
	for _, d := range Dimensions {
		if res.Scores[d] > 0 && !supported[d] {
			fail("%s has a non-zero score without supporting evidence", d)
		}
	}
	if len(res.Problems) > 0 {
		return res
	}
	res.Valid = true
	res.Evidence = out.Evidence
	res.Recommendation = out.Recommendation
	res.OutputRisk = out.HallucinationRisk.Severity
	res.RoutingReasons = routing(out, a)
	return res
}

// routing lists why a reviewer should look at this answer. Every answer is reviewed in this phase
// (ADR-022); the reasons tell the reviewer what to look at first.
func routing(out output, a Answer) []string {
	reasons := []string{"policy_all_reviewed"}
	add := func(cond bool, reason string) {
		if cond {
			reasons = append(reasons, reason)
		}
	}
	add(out.Confidence.Score < ReviewThreshold, "low_confidence")
	add(a.MissingSegments || out.TranscriptQuality.MissingSegments, "missing_segments")
	add(out.TranscriptQuality.Score < TranscriptQualityThreshold, "transcript_quality")
	add(len(a.IntegrityFlags) > 0, "integrity_flags")
	add(out.ReviewRequired || out.Recommendation == "HUMAN_REVIEW", "evaluator_requested_review")
	add(out.Recommendation == "FOLLOW_UP", "follow_up_suggested")
	add(out.HallucinationRisk.Severity != "low", "evaluator_output_risk")
	add(len(out.UnsupportedClaims) > 0, "unsupported_claims")
	add(len(out.Contradictions) > 0, "contradictions")
	return reasons
}

// Disagree reports whether two evaluations of one answer differ by more than DisagreementPoints on
// any dimension (PROMPT_AND_EVALUATION section 6: independent evaluators that materially disagree).
func Disagree(a, b map[string]int) bool {
	for _, d := range Dimensions {
		if diff := a[d] - b[d]; diff > DisagreementPoints || -diff > DisagreementPoints {
			return true
		}
	}
	return false
}

// WeightedCenti is the weighted score of reviewer or evaluator scores, in hundredths of a point.
func WeightedCenti(scores map[string]int, r *Rubric) (int, error) {
	total := 0
	for _, d := range Dimensions {
		v, ok := scores[d]
		if !ok || v < 0 || v > 100 {
			return 0, fmt.Errorf("%s must be an integer between 0 and 100", d)
		}
		total += v * r.Weights[d]
	}
	if len(scores) != len(Dimensions) {
		return 0, errors.New("scores must contain exactly the four dimensions")
	}
	return total, nil
}

// normalize applies Unicode NFC and collapses whitespace: the only differences a verbatim quote may
// have from the transcript (case and punctuation must match).
func normalize(s string) string { return strings.Join(strings.Fields(norm.NFC.String(s)), " ") }

// quoteFound reports whether a quote appears verbatim in the cited segments' texts, given in
// transcript order and joined by one space. The review console highlights quotes by the same rule
// (apps/web/src/lib/staff/highlight.ts); contracts/fixtures/quote-matching holds the cases both test.
func quoteFound(texts []string, quote string) bool {
	return strings.Contains(normalize(strings.Join(texts, " ")), normalize(quote))
}

// schemaProblems reports where the output breaks the contract, without echoing its values.
func schemaProblems(err error) []string {
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return []string{"output does not match the evaluation contract"}
	}
	var out []string
	for _, u := range ve.BasicOutput().Errors {
		if u.Error == nil || u.KeywordLocation == "" {
			continue
		}
		out = append(out, fmt.Sprintf("schema: %s at %s", u.KeywordLocation, orRoot(u.InstanceLocation)))
		if len(out) == 10 {
			break
		}
	}
	if len(out) == 0 {
		out = append(out, "output does not match the evaluation contract")
	}
	return out
}

func orRoot(loc string) string {
	if loc == "" {
		return "/"
	}
	return loc
}
