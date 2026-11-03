# ADR-018 — Text response mode

**Status:** Proposed (owner-approved direction, 2026-11-03) — pending architecture review
**Date:** 2026-11-03
**Relates to:** BLUEPRINT §12, §18; THREAT_MODEL_AND_QA §4–5, §7

## Context

The QA matrix requires a text-only alternative, and the privacy controls say audio should not be collected when text is sufficient and chosen. A candidate who denies microphone access, or has no microphone, would otherwise be unable to complete the interview. The roadmap assigns this to no phase.

## Decision

1. Input mode (`voice` | `text`) is chosen at the device check and fixed at `start`. Text mode is offered whenever the microphone is unavailable or the voice-processing purpose was declined.
2. In text mode the candidate's LiveKit token **cannot publish any track**, so no candidate audio is collected or recorded. The interviewer still speaks and is captioned.
3. Text answers go through `POST /api/v1/interviews/{id}/text-responses` (candidate principal, activation-bound). The **control plane** writes them as candidate segments (`source = candidate_text`, server timestamps, producer `control_plane`). They never pass through the data channel (`canPublishData = false`).
4. The agent receives them as `pending_text_inputs[].untrusted_text` in the Tier-1 context (view `tier1_text_inputs_v`: text answers to the open question only, newest 20) and passes each one to the model once, as user input (`generate_reply(user_input=...)`), never in instructions. They are subject to the same injection controls as speech. A text answer is refused while a completion request is pending, so the finalized answer never misses a late text.
5. State transitions, completion protocol and attribution are identical to voice mode. CANDIDATE_SPEAKING simply means "answer in progress".

## Consequences

The accessibility gap closes without weakening isolation. Text answers are written directly by the trusted control plane, which strengthens their provenance. A fully text interviewer (no audio at all) and a broader accommodations process remain Phase 5/6 items.

## Verification

API tests for activation binding and mode checks, a test that the text-mode token has no publish permission, agent tests that text arrives as user input, and Playwright for the text path.
