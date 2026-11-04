# Data and ML lifecycle (Tier-2 evaluation)

**Status:** Draft engineering record, written 2026-11-04. It is not a legal assessment, and no benchmark has been run:
no human labels exist yet, and the only evaluator that runs in this repository is the deterministic reference
evaluator. This document says how evaluator quality will be measured and governed before the
`evaluation_validity` production gate (BLUEPRINT §20) can be considered. It relates to ADR-007, ADR-008, ADR-009,
ADR-013, ADR-021 and ADR-022, PROMPT_AND_EVALUATION §4–8, STACK_AND_BENCHMARK §4 and THREAT_MODEL_AND_QA §5–6.

## 1. Principles

- An evaluator's output is a claim to verify, never a result to trust. The control plane validates every attempt
  against the stored transcript (ADR-022), and every answer is reviewed by a person until the evaluation-validity
  gate is passed.
- Quality is measured against human judgement on a versioned, documented dataset, not asserted. Until then,
  confidence stays `calibrated: false` and the routing thresholds stay policy values.
- Benchmarks use synthetic or licensed data, or candidate data only where a reviewed lawful basis and consent cover
  that use. Interview data is never reused for benchmarking by default.
- No new infrastructure without evidence. A vector store (ADR-005's Qdrant projection), embeddings, retrieval or
  fine-tuning are introduced only after a benchmark shows they improve agreement or evidence validity at an
  acceptable cost, and an ADR records the decision.
- No inference the product must not make: no voice biometrics, emotion, personality, sincerity, accent or cheating
  inference, and no automatic adverse decision (BLUEPRINT §12, ADR-010).

## 2. Lineage

Every stored number can be traced to its inputs. The chain, from the report back to the speech:

| Record | Carries | Points to |
|---|---|---|
| `reports.body` | Reviewed scores, rationale, review reasons, the AI draft it started from | review outcomes, evaluations |
| `review_outcomes` | Reviewer, scores, rationale, time, rubric version, prior AI scores | review task, prior evaluation |
| `evaluations` | Validated scores, recomputed weighted score, evidence, routing reasons | the attempt it came from |
| `evaluation_attempts` | Raw output as received, outcome and problems, provider, model ID, prompt hash, evaluator version, request ID, tokens, latency | answer, transcript version, rubric version |
| `candidate_answers` | Segment IDs, transcript version, integrity flags | transcript segments |
| `transcript_segments` | Speaker, text, timings, source, ASR model, engine | the session |
| `rubrics` | Immutable rubric version and its sha256 | — |

- **Versions.** An evaluator version names the provider, the model and the hash of the complete system
  instructions, so a prompt change creates a new evaluation rather than overwriting one (INV-2).
- **Benchmark datasets.** These get the same treatment:
  - each dataset version is a manifest listing every item with its sha256, and is immutable once published;
  - a benchmark run records the dataset version, the evaluator version, the validator commit and the date.

## 3. Datasets

| Dataset | Content | Use | Status |
|---|---|---|---|
| `synthetic-v1` (`services/tier2-evaluator/bench/`) | Synthetic answers in English and Turkish to the demo questions, written for the purpose, with **synthetic labels** | Verifies the harness, the validator and the metrics; says nothing about model quality | Shipped |
| Labelled evaluation set | Answers from consented, licensed or commissioned sessions, labelled under section 4 | Evaluator agreement, error analysis, calibration, subgroup review | Not started: needs labels, a lawful basis and a labelling budget |
| Locked test set | A held-out part of the labelled set, opened only for release decisions | Final measurement before a registry status change or a gate review | Not started |

Rules for every non-synthetic dataset:
- **Splits.** Split by session, never by answer, so that a candidate's answers never span splits. Stratify by
  language (Turkish, English and code-switching), question and answer-quality band.
- **No tuning on the test set.** Prompts, thresholds and rubric wording are tuned on development data only.
- **Minimisation.** Candidate identity is removed before labelling (the evaluator role never sees it anyway); free
  text that identifies a person is redacted, and the redaction is reviewed.
- **Retention.** A dataset's retention and deletion path are recorded in the data inventory before it is created.

## 4. Labelling protocol

- **Raters.** At least two independent raters per answer, qualified in the subject. They are trained on the rubric
  version and blind to each other and to any AI output.
- **Task.** Each rater scores each dimension from 0 to 100 against the rubric's criteria and cites the transcript
  segments that support each judgement, as reviewers do in the product.
- **Adjudication.** A third rater adjudicates every dimension where the raters differ by more than 20 points; the
  adjudicated score is the reference label. Every rater decision is kept: disagreement is data.
- **Reliability.**
  - Agreement between raters is reported per dimension, before adjudication:
    - quadratic-weighted Cohen's kappa for two raters, on scores binned into five bands of 20 points;
    - Krippendorff's alpha (interval) for any number of raters, which also tolerates missing ratings.
  - A dimension whose raters cannot agree cannot be used to judge an evaluator. Working targets are hypotheses to
    revisit with reviewers: alpha of at least 0.67 for tentative conclusions, and 0.80 for decisions.
- **Records.** Each label records the rater's pseudonymous ID, the time, the rubric version and the time spent.
  Raters' own consent and working conditions are part of the protocol's review.

## 5. Metrics

The harness computes:

| Metric | Definition |
|---|---|
| Evidence validity | Share of evaluator attempts that pass the control plane's validator (`internal/evaluation`), with the distribution of problems |
| Abstention rate | Share of answers the evaluator abstained on, by reason |
| Score agreement | Per dimension against the reference label: mean absolute error, share within 10 points, quadratic-weighted kappa on five bands |
| Rater reliability | Kappa and alpha between raters (section 4), so that agreement with the evaluator is read against agreement between people |
| Latency | Evaluator latency per answer, p50 and p95 |
| Cost | Input and output tokens per answer; prices are recorded with their date, never hard-coded (STACK_AND_BENCHMARK §5) |

Once enough labels exist, these are added:
- calibration of the evaluator's confidence, with a reliability diagram and expected calibration error per evaluator
  version;
- disagreement between independent evaluators.

## 6. Fairness review

- Subgroup analysis uses only attributes that are needed, disclosed, legally reviewed and collected with consent.
  The interview language is product data; anything else is not inferred.
- The review looks for systematic score differences between subgroups at equal reference labels. It also compares
  error rates and abstention rates between subgroups, and checks whether transcription quality causes
  evaluation differences (for example by language).
- A finding blocks use of the evaluator version until a person has reviewed it.

## 7. Governance of changes

| Change | Requires |
|---|---|
| New evaluator model, or registry status `candidate` → `verified` | A recorded live run on synthetic interviews, a benchmark run on the labelled set, and review of both |
| Prompt or output-rule change | A new evaluator version (automatic), and a benchmark run before it is enabled |
| Threshold change (confidence, transcript quality, disagreement) | Calibration evidence on the labelled set |
| Retrieval, embeddings, a vector store or fine-tuning | A benchmark showing an improvement, and an ADR |
| Removing mandatory human review for any answer | Passing the evaluation-validity gate and governance approval; T16 stays unwired until then |

In production, the evaluator's behaviour is monitored for drift. The signals are:
- schema and validation failure rates;
- abstention rate;
- reviewer override rate per evaluator version;
- evaluator disagreement.

## 8. What this repository verifies today

- The harness runs end to end on `synthetic-v1` with the reference evaluator (`make tier2-bench`). Its metric
  functions are unit-tested against hand-computed values and Krippendorff's published example (`make tier2-test`).
- Result on 2026-11-04:

```text
dataset synthetic-v1 v1 (15 items, synthetic: True); evaluator reference@1
evidence validity 14/14; abstained 1 {'no_candidate_speech': 1}
dimension                raters kappa raters alpha    MAE  within 10  kappa
technical_accuracy              0.958        0.967   12.9      0.357   0.98
architecture_maturity           0.988        0.994   13.7      0.429  0.959
edge_case_awareness             0.958        0.966   11.1        0.5  0.969
claim_support                    0.97         0.99   15.5        0.5  0.918
Synthetic labels: these numbers check the harness, not evaluation quality.
```

- These results check the mechanics only. The labels are synthetic, and the reference evaluator matches phrases.
  Neither says anything about how well any model evaluates technical answers.
