# ADR-012 — Interview state machine v1.1 and answer completion protocol

**Status:** Proposed (owner-approved direction, 2026-11-03) — pending architecture review
**Date:** 2026-11-03
**Relates to:** BLUEPRINT §4, §6, §18; contract `contracts/state-machine/interview-session.v1.1.json`

## Context

The BLUEPRINT §4 diagram (v1) allows `PAUSED` only from `QUESTION_ACTIVE` and has no withdrawal or expiry path. Disconnects and accessibility pauses usually happen during calibration or while the candidate is answering. The diagram also leaves several things unspecified: who may trigger each transition, how an answer is closed safely when transcripts arrive late, and what happens after the last answer.

## Decision

1. **Contract v1.1** keeps every diagram transition and adds the following, each tagged `source: ADR-012` and checked by a drift test against the diagram:
   - `CALIBRATION → PAUSED`, `CANDIDATE_SPEAKING → PAUSED`; `PAUSED → CALIBRATION` when `paused_from = CALIBRATION`.
   - Candidate withdrawal `→ CANCELLED` from READY, CALIBRATION, QUESTION_ACTIVE, CANDIDATE_SPEAKING and PAUSED.
   - System expiry `PAUSED → CANCELLED` after a configurable pause timeout (product policy; the default is a documented placeholder).
2. **Resume** returns to `QUESTION_ACTIVE` with a **new activation of the same plan item**, or to CALIBRATION. The answer to a plan item is the union of its activations' evidence-eligible segments. Pausing closes the open activation, cancels any pending completion request, stops audio intake and stops recording.
3. **Actors per transition** are part of the contract: candidate, tier1_agent, system, tier2_pipeline, reviewer. Every tier1_agent transition is also available to the candidate, directly or through a candidate request (INV-8). tier2_pipeline and reviewer have no pre-interview or live transitions (INV-6).
4. **Answer completion protocol (request R01).** The tool path and the candidate's "Finish answer" button create a completion *request*, never a direct transition. Each request carries `activation_id` and `state_version`, so a stale or replayed request (for example a late click after the next question opened) is rejected.
   - Tool path: the agent waits for a speech-free grace period, flushes pending transcriptions, then sends the request with a flush manifest. The server finalizes atomically once every manifest segment is present.
   - Button path: the server records the request with a deadline. The agent flushes and confirms the manifest. If the deadline passes first, a sweeper finalizes with `missing_segments=true`.
   - Finalization (T07) and the following chain step (T08+T09, T10 or T11) happen in **one transaction**, so ANSWER_PROCESSING and FOLLOW_UP are never observed at rest (INV-9). Each step is still recorded as its own session event with its causing actor (INV-7).
5. **After the last answer** the chain goes straight to INTERVIEW_COMPLETED (T11, `plan_exhausted`). Closing remarks happen after completion. `POST /complete` is idempotent and only confirms the state; early termination is withdrawal.
6. **Follow-ups** come only from the pinned plan and are unconditional in v1 (`policy: always`). The flow engine never consumes Tier-2 output (conflict C5 remains open for Phase 3).
7. CANCELLED sessions never emit `interview.completed` (INV-10). Withdrawal and expiry delete the LiveKit room so stale tokens cannot rejoin.

## Consequences

- Answer evidence is never cut short by a click or a premature tool call, and late transcripts are kept but flagged rather than silently attached to the wrong question.
- The diagram in BLUEPRINT §4 is now a subset of the contract. A generated Mermaid diagram (`interview-session.v1.1.mmd`) is the reviewable picture of v1.1. If this ADR is accepted, BLUEPRINT §4 should reference it.

## Verification

Contract tests (structure, invariants, drift against the diagram), Go model-based tests of the engine, concurrency tests for stale and duplicate requests, and E2E pause, resume and withdraw scenarios.
