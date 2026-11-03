# ADR-015 — Transactional outbox as the only Tier-1 to Tier-2 handoff

**Status:** Proposed — pending architecture review
**Date:** 2026-11-03
**Relates to:** ADR-001, ADR-006, BLUEPRINT §3.1; contracts `answer.finalized/v1`, `interview.completed/v1`

## Context

ADR-006 selects Redis Streams for durable evaluation jobs (Phase 2). If an event is published to a broker outside the transaction that finalizes the answer, a crash between the two can drop the event or publish one for an answer that was never committed. Tier-2 also needs a narrow, well-defined input so it cannot depend on Tier-1 internals or receive data it should not have.

## Decision

1. Finalizing an answer (T07) and completing the interview (T11) insert `answer.finalized.v1` / `interview.completed.v1` rows into an `outbox_events` table **in the same transaction** as the state change.
2. Payloads carry **identifiers only**: session, answer and answer_version, plan, item and parent item, rubric reference, transcript version, segment IDs, flags and finalization cause. They carry no transcript text, no quotes and no interaction timing. Tier-2 reads evidence through its own least-privilege database role (`ati_tier2_evaluator`), which is NOLOGIN until Phase 2 and can never write live-interview tables.
3. Phase 2 adds a relay (outbox → Redis Streams, consumer groups, retries, DLQ, reclaim). Phase 1 only writes and tests the outbox rows.
4. CANCELLED sessions never get `interview.completed`. Tier-2 consumers must also skip answers of sessions that end CANCELLED.
5. The live flow engine has no read path from evaluation data. Tier-2 output can affect only post-interview states, and only through the control-plane API (contract INV-6).

## Consequences

Exactly-once *recording* of handoff events, and at-least-once *delivery* once the relay exists (consumers deduplicate by `event_id`). Evaluation cannot sit on the voice path because nothing in that path reads it.

## Verification

Integration tests check that outbox rows exist only for committed finalizations, match the event schemas, contain no text or timing, and never appear for cancelled sessions. Role privilege tests cover the evaluator role.
