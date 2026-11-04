# synthetic-v1

A **synthetic** benchmark dataset for the offline harness (`python -m tier2_evaluator.bench`,
[DATA_AND_ML_LIFECYCLE.md](../../../../docs/architecture/DATA_AND_ML_LIFECYCLE.md)). It exists to verify the
harness, the control plane's validator and the metrics. It says nothing about how well any evaluator assesses
technical answers.

- **Answers.** Fifteen answers in English and Turkish to the three demo-plan questions. They were written for this
  purpose and contain no candidate data. They range from strong to weak, and include:
  - two raters who disagree, settled by an adjudicator;
  - an injected instruction;
  - an answer with no candidate speech, on which the evaluator must abstain.
- **Labels.** Also synthetic. `rater_a` and `rater_b` are invented scores, not human ratings. `reference` is their
  mean (rounded half up) where they are within 20 points, and otherwise the `adjudicated` score.
- **Splits.** Every answer belongs to a synthetic session (`session_key`). Splits are assigned per session, so the
  harness can check that no session spans splits.
- **Integrity.** `manifest.json` pins each item's sha256; the harness refuses a dataset that does not match its
  manifest. Do not edit an item in place: publish a new dataset version.
- **Rubrics.** The answers are scored against `contracts/content/rubrics/` (synthetic demo rubrics, version 1).
