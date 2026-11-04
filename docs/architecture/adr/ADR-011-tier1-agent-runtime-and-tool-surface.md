# ADR-011 — Tier-1 agent runtime, voice engines and tool surface

**Status:** Proposed — pending architecture review
**Date:** 2026-11-03
**Relates to:** ADR-001, ADR-004, BLUEPRINT §3.1, §6, §8

## Context

The blueprint places the realtime voice agent in the realtime plane and assigns "AI orchestration" to Python workers, but does not say where the Tier-1 agent runs, what it may call, or how a provider adapter is shaped. LiveKit Agents (Python) is the maintained agent framework for LiveKit. Several of its defaults conflict with this platform's privacy and isolation requirements: recording follows a server-side job setting when `record` is not passed, sessions close when the linked participant disconnects, participant text input is enabled, and the worker inherits `HTTPS_PROXY` without honouring `NO_PROXY`.

## Decision

1. The Tier-1 agent is a **separate Python deployable** (`services/tier1-voice-agent`) with its own lockfile. It contains no database driver and no Tier-2 code (enforced by dependency and import checks). It talks to the control plane only through the internal Tier-1 API (ADR-016).
2. It runs on `livekit-agents` `AgentServer` with an explicit `agent_name` (explicit dispatch only) and `AgentSession` with these settings fixed in code and unit-tested:
   - `record=False`: no framework-level audio or transcript recording. Audio recording, when consented, is done by the control plane through egress (ADR-017).
   - `RoomOptions(participant_identity="candidate", close_on_disconnect=False, text_input=False)`: link only to the candidate, survive reconnects, and accept no data-channel text (candidate text goes through the control plane, ADR-018).
   - `http_proxy` set explicitly from configuration. It is never inherited from the environment.
3. **Application-owned engine interface** (`VoiceEngine`) with two implementations:
   - `openai`: `RealtimeModel` (speech-to-speech) with language, transcription model and turn-detection parameters taken from the plan modality. Each activation applies its question's turn profile to the live provider session (semantic VAD eagerness `medium` for `standard`, `low` for `extended_pause`). The parameters are uncalibrated hypotheses. The engine refuses to start without a key and is unverified until run against the live provider.
   - `scripted`: fake STT, LLM, TTS and VAD plugins running *inside* `AgentSession`, so interruption handling, transcript sync and tool calls take the same framework path. It produces `source=synthetic_test` transcripts, which flag every answer built from them so that answer is never evaluated. The engine is refused in production (ADR-020). It also carries adversarial scripts and failure-injection scripts (connection error, mid-stream drop, a model that never answers, speech-recognition and speech-synthesis failures, a score disclosure for the output monitor). Its turn profiles set the endpointing delay (0.3 s `standard`, 1.2 s `extended_pause`).
4. **Tool surface:** the language model gets exactly one tool, `mark_answer_complete`, with **no arguments**. Its description, not the system prompt, carries the usage policy: confirm with the candidate first, and never use it while they are speaking. The tool only issues request R01 (ADR-012). Deterministic agent code adds `activation_id` and `state_version`, waits for a speech-free grace period measured on received silent frames, and flushes pending transcriptions before sending. Everything else (calibration, pause on provider failure, transcript forwarding) is deterministic code that the model cannot reach.
5. The Tier-1 system prompt is the verbatim versioned template (`contracts/prompts/tier1-interviewer/v1/system.txt`), checked against its sha256 pin at start-up. Runtime context is appended as a clearly delimited data block built only from the Tier-1 context allowlist (`tier1-context/v1`). Candidate text never enters instructions.
6. The Tier-1 context collapses every post-interview state (EVALUATION_PENDING, HUMAN_REVIEW, REPORT_READY) to INTERVIEW_COMPLETED, because the agent must not learn evaluation or review outcomes.

## Consequences

- Prompt injection can reach at most R01, which is equivalent to the candidate ending their own answer. No score-write, plan-change or skip capability exists for the agent.
- The scripted engine verifies the framework path on loopback but **not** the production speech-to-speech interruption path, which differs (provider-side VAD). Status reports must say "scripted engine, loopback only".
- A native speech-to-speech model cannot be validated before it speaks. Output checks are post-hoc: interviewer segments are stored as actually played, with similarity to the approved prompt and a deviation flag (ADR-014).
- LiveKit agent workers hold LiveKit API credentials, which can mint room tokens. Workers are trusted infrastructure: they use a dedicated key pair, are network-isolated, and unexpected participants are alerted on (ADR-016).

## Verification

Agent unit tests cover session settings, tool schema (no parameters), prompt pin, context rendering and failure injection. Dependency and import-boundary checks run in `make checks`. E2E with the scripted engine runs over real WebRTC.
