# ADR-014 — Session timeline, transcript ingestion, attribution and provenance

**Status:** Proposed — pending architecture review
**Date:** 2026-11-03
**Relates to:** BLUEPRINT §4.1, §13, §18; contracts `transcript-segment/v1`, `segment-submission/v1`

## Context

Transcripts are the evidence Tier-2 will score. They arrive asynchronously, sometimes after the speech they describe, may be retried or duplicated, and in the MVP are produced inside the Tier-1 provider session. The blueprint diagram, by contrast, shows transcription independent of the voice agent (RTC → STT). If a segment is assigned to whichever question is active when it *arrives*, a candidate who keeps answering question 1 over question 2's prompt has that speech recorded under question 2.

## Decision

1. **Timeline.** Every session has a server-recorded epoch, set at `start`. Segment `start_ms`/`end_ms` are milliseconds on that timeline. The agent corrects for clock skew using `server_now_unix_ms` from each context response. The server rejects segments that end in the future (beyond a tolerance), that have `end_ms < start_ms`, or that start inside a recorded pause interval (rejections are integrity events).
2. **Idempotent ingestion.** The producer supplies `segment_key`. A retry with the same key and identical content returns the existing segment. The same key with different content returns 409 and records an integrity event. The server assigns the per-session `sequence`. Out-of-order arrival is accepted; canonical order is (`start_ms`, `sequence`).
3. **Attribution by speech time, decided by the server.**
   - Each question activation has an answer window that opens when the agent reports the prompt started playing (`prompt-started`) and closes at finalization or pause.
   - A candidate segment is attributed to the activation whose window contains its `start_ms`. Otherwise it is stored with `question_id = null` and flagged `unassigned`.
   - The agent cannot set `question_id`, `activation_id`, `turn_id`, `sequence` or `evidence_eligible`; the submission schema has no such fields.
   - Calibration segments are stored with `evidence_eligible = false`.
4. **Flags are review triggers, never score inputs:** `possible_echo` (overlapping interviewer playback), `low_energy`, `unassigned`, `prompt_deviation` (interviewer segment similarity to the approved prompt below a threshold, which is uncalibrated) and `late_arrival` (arrived after its answer was finalized).
5. **Interviewer segments** are stored as actually played, truncated on interruption with `interrupted = true`. This lets Tier-2 evaluate against what the candidate actually heard.
6. **Provenance on every segment:** producer, lease ID, fencing token, provider item ID, ASR model and engine. Every segment has `quality = unverified` in the MVP. `source = synthetic_test` marks scripted-engine output, which is never evidence.
7. **Append-only.** Segments and answers are never updated or deleted by application roles; database grants and triggers enforce this. A revision is a new `transcript_version` row set. An answer records the transcript version and the exact segment IDs it was built from, so evidence used by an evaluation can never change silently.
8. **Accepted MVP risk:** evidence transcription comes from the provider session inside Tier-1 (OpenAI documents realtime input transcription as a separate ASR pass and a rough guide). Mitigations are provenance, `quality = unverified`, recording for later re-transcription (ADR-017), and room for a Tier-2-owned transcriber producing `transcript_version = 2`.

## Consequences

Evidence is attributed by when it was spoken, not when it arrived. Misattribution becomes visible through flags instead of silent. A Turkish and code-switching WER benchmark is still required before production (STACK §4).

## Verification

Integration tests against PostgreSQL cover duplicates, conflicting retries, out-of-order arrival, segments in pauses, unassigned speech, late arrival, and append-only grants and triggers. E2E covers reconnect and transcript integrity.
