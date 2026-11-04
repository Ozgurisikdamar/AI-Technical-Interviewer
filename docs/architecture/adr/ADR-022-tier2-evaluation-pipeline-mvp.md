# ADR-022 — Tier-2 evaluation pipeline MVP: outbox relay, Redis Streams, deterministic validation, mandatory human review

**Status:** Proposed — pending architecture review
**Date:** 2026-11-04
**Relates to:** ADR-001, ADR-005, ADR-006, ADR-007, ADR-008, ADR-009, ADR-012, ADR-013, ADR-015, ADR-021; PROMPT_AND_EVALUATION §3–8; conflicts C3, C4, C5

## Context

- Phase 1 writes `answer.finalized` and `interview.completed` events to the outbox. They carry identifiers only (ADR-015), nothing consumes them, and sessions stop at `INTERVIEW_COMPLETED`.
- ADR-006 (accepted) chooses Redis Streams with consumer groups, acknowledgements, retries, idempotency and a dead-letter path.
- On scoring, ADR-007 and PROMPT_AND_EVALUATION §4 require the backend to validate evaluator output and to recompute the weighted score. §6 says confidence is uncalibrated and lists the conditions that route to human review.
- There is no human-labelled benchmark, so evaluator quality is unknown. No governance body has approved automatic reports.
- In state-machine contract v1.1, these transitions are deferred:
  - T14 (actor `system`) and T15/T16 (actor `tier2_pipeline`) are Phase 2;
  - T17 (actor `reviewer`) is Phase 5.

## Decision

1. **Handoff.** The control plane's relay publishes outbox events, in `seq` order, to the Redis stream `ati:tier2:events`. The entry carries the stored, contract-validated payload: identifiers only.
   - For `interview.completed`, the relay first applies T14 (`INTERVIEW_COMPLETED → EVALUATION_PENDING`, actor `system`). Then it adds the entry, then it marks the row `published_at`.
   - If the relay crashes between steps, the event is published again. Consumers are idempotent, so this is harmless.
   - The relay is off when `ATI_REDIS_URL` is unset, as in Phase 1 deployments.
2. **Consumption.** `services/tier2-evaluator` reads the stream with the consumer group `tier2`:
   - It acknowledges an entry after processing it.
   - It reclaims entries left pending longer than the visibility timeout (`XAUTOCLAIM`).
   - After five deliveries it moves the entry to `ati:tier2:dlq` and reports the failure. The session then falls back to human review.
   - `answer.finalized` entries are acknowledged without work. Evaluation starts only once the interview has completed:
     - Tier-2 never acts on a live interview (INV-6);
     - cancelled sessions never emit `interview.completed` (INV-10).
3. **Evidence access.**
   - The worker reads evidence through `ati_tier2_evaluator`, which can log in from Phase 2. Its grants are SELECT on answers, segments, questions and rubrics, plus a narrow session view.
   - It cannot see candidate identity and cannot write anything.
   - Its results reach the database only through the control plane's internal API (ADR-015), authenticated with a Tier-2 service token.
4. **Idempotency.**
   - A canonical evaluation is unique per answer, transcript version, rubric and version, and evaluator version (INV-2).
   - Every attempt, valid or not, is stored raw in `evaluation_attempts`, separately from the canonical `evaluations` (PROMPT_AND_EVALUATION §5).
5. **Validation**, in trusted code (`internal/evaluation`) on top of the JSON schema (evaluation v1, ADR-013):
   - **Identity:** `session_id`; `question_id` is the plan item; `evaluation_version` is `rubric_id@version`.
   - **Scores:** exactly the four dimensions, as integers from 0 to 100.
   - **Weighted score:** recomputed with the rubric's weights, which must equal the documented ones, and matched to within 0.01.
   - **Cited segments:** every one belongs to the answer, meaning evidence-eligible candidate speech of this transcript version.
   - **Quote:** appears verbatim in the text of the cited segments. Comparison applies only Unicode NFC and whitespace collapsing, and is case-sensitive.
   - **Span:** the millisecond span lies within the cited segments.
   - **References:** every reference ID belongs to the rubric.
   - **Support:** a non-zero dimension needs at least one supporting evidence item (`correct` or `partially_correct`).

   Any failure makes the attempt invalid; it is stored together with its reasons. The answer then reaches human review without an AI score.
6. **Routing.** In this phase every answer goes to human review (T15). The reasons are recorded:
   - `policy_all_reviewed`, always;
   - confidence below 0.85. This is a policy threshold; the outputs carry `calibrated: false`;
   - missing segments, integrity flags, an abstention, invalid output, or a pipeline failure;
   - evaluator disagreement, when two evaluators run.

   T16 (automatic report) stays unwired until a benchmark and governance approval exist.
7. **Human review.** T17 moves from Phase 5 to Phase 2 in state-machine contract v1.2.
   - Each answer gets a review task. A staff member with the `reviewer` or `admin` role submits a score per dimension and a rationale.
   - An answer whose question has no rubric (a plan may leave `rubric_ref` null) cannot be scored. It is still reviewed: the reviewer records a rationale only, and scores are refused. Without this, such an interview could never leave `HUMAN_REVIEW`. Follow-up questions inherit their parent's rubric.
   - The outcome records the reviewer, the prior AI scores, the revised scores, the rationale, the time and the rubric version (PROMPT_AND_EVALUATION §6).
   - When every task of a session is complete, T17 moves the session to `REPORT_READY` and a report snapshot is written.
8. **Report.**
   - It is staff-only: tenant-scoped, for the `admin`, `recruiter` and `reviewer` roles, and only at `REPORT_READY`.
   - It is built only from canonical data: the review outcomes, plus verified AI evidence marked as cited by the AI.
   - It has no hiring decision, recommendation or ranking, and is labelled decision support.
   - Candidates see no scores.
9. **Conflicts.**
   - **C3:** `hallucination_risk` is the risk that the evaluator's own output is unsupported by the evidence. It is never a property of the candidate, and it is stored but not scored.
   - **C4:** the prompt hash, schema, rubric, evaluator, model, and answer and transcript versions live in the database record of each attempt and evaluation. `evaluation_version` in the output is the rubric reference.
   - **C5:** `recommendation: FOLLOW_UP` is advisory and only recorded. Live follow-ups come only from the plan.
10. **Evaluators.**
    - A deterministic reference evaluator works only on synthetic rubrics and is refused in production. Tests and E2E use it.
    - The OpenAI adapter uses the Responses API with Structured Outputs; the Anthropic adapter uses the Messages API with structured outputs. Both take text only, and their model IDs come from the registry under `tier2_evaluator` (ADR-021).
    - Anthropic's server-side refusal fallback is not enabled: it would change which model produced an evaluation. A refusal is recorded, and the answer goes to review.
    - There is no vector store, no embeddings and no fine-tuning. ADR-005's Qdrant projection waits for a benchmark showing that retrieval helps.

## Consequences

- An interview now ends at `REPORT_READY` only through human review. Throughput is bounded by reviewer capacity, deliberately so until evaluator agreement with human labels is measured (Milestone C).
- Phase 2 relaxes four guard tests that enforced its absence, and each is replaced by a narrower test that keeps the invariant:
  - control-plane evaluation code may exist, but must not depend on the media or Tier-1 packages;
  - the evaluator role may log in, but still reads only evidence and writes nothing;
  - the API may expose review and report operations to staff, but never a hiring decision and never candidate-facing scores;
  - the flow engine runs up to Phase 2.
- Raw evaluator output contains quotes of candidate speech. It carries a retention date like the other assessment output (`ATI_RETENTION_ASSESSMENT_OUTPUT_DAYS`). The purge procedure is part of gap G1 of the data inventory.
- The model evaluators cannot be verified here (no provider keys). Their adapters are tested against recorded provider responses only.

## Verification

- Validator tests cover each item of PROMPT_AND_EVALUATION §8 that applies to Phase 2:
  - schema fixtures; score bounds and arithmetic; unknown rubric version;
  - timestamps beyond the transcript; spans from the interviewer; empty transcripts;
  - duplicate delivery; retry after a timeout; confidence routing;
  - a transcript revision creating a new evaluation; unsupported claims;
  - injection text in the transcript; evaluator disagreement; override audit integrity.
- Store tests check the grants and the append-only tables.
- API tests check RBAC, tenant scoping and the absence of decision fields.
- Worker tests run against a real local Redis: acknowledgement, reclaim, the DLQ and idempotent redelivery.
- End to end on the local stack (`tests/e2e/test_tier2.py`):
  - a text-mode interview, then evaluation, review and report;
  - a redelivered completion;
  - a dead-lettered entry falling back to human review;
  - scripted (synthetic) voice interviews whose answers are all abstained on and still reviewed.
