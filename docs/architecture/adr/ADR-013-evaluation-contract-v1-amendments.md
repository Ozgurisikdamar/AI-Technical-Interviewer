# ADR-013 — Evaluation contract v1 amendments

**Status:** Proposed (owner-approved direction, 2026-11-03) — pending architecture review
**Date:** 2026-11-03
**Relates to:** ADR-007, BLUEPRINT §7, PROMPT_AND_EVALUATION §3–4

## Context

The v1.0 schema named the fourth score dimension `evidence_honesty`. BLUEPRINT §7 states that this dimension measures support for technical claims, not inferred sincerity, and that a candidate must never be labelled dishonest. A field name containing "honesty" invites exactly that misreading in reports and reviewer screens. Separately, P&E §4 requires exact quote and span verification, but v1.0 evidence items held only free-text `claim` and millisecond bounds. A verbatim check was therefore impossible. No component consumes the schema yet, so amending it now costs nothing downstream.

## Decision

Amend v1 before freezing (documented in PROMPT_AND_EVALUATION v1.1):

1. Rename `evidence_honesty` → `claim_support` in `scores`, in the evidence `dimension` enum and in the validator sketch weights. The weight (0.15) and the blueprint formula are unchanged.
2. Add required evidence fields:
   - `transcript_segment_ids` (non-empty, unique): the candidate segments the judgement is based on.
   - `quote`: a verbatim excerpt from those segments. A Draft 2020-12 `if/then/else` requires a non-empty quote unless `assessment = not_addressed`, in which case the quote must be empty and the segment IDs list the answer segments examined.
3. The backend validator (Phase 2) must check that every non-empty quote occurs verbatim in the referenced candidate segments, that those segments belong to the evaluated answer and transcript version, and that the millisecond bounds cover them.

## Consequences

Evidence becomes mechanically verifiable against stored, append-only segments (ADR-014). Evaluator prompts in Phase 2 must ask for verbatim quotes and segment IDs.

## Verification

Schema fixtures cover the legacy name, missing quote, empty quote for assessed claims, a quote on not_addressed, and empty segment IDs. Drift tests keep the document block, schema file and weights identical.
