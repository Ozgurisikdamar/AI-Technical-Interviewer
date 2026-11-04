# Data inventory and data flows

**Status:** Draft engineering record, written 2026-11-04 from the code and updated the same day for the Tier-2
evaluation MVP (ADR-022). It is not a legal assessment. The
privacy and legal review (BLUEPRINT §20 gate `privacy_legal`) has not happened; nothing here says a lawful basis
exists. Every candidate notice in the repository is a DRAFT with basis `to_be_determined_by_legal`.

This document answers four questions:
- what personal data the system handles;
- where that data goes, and who processes it;
- who can read it;
- how long it is kept and how it is deleted.

The gaps it identifies are listed at the end and tracked in the
[implementation status](../status/IMPLEMENTATION_STATUS.md).

## Data flows

```text
 staff (staff token) ──HTTPS──► control plane (public API) ──► PostgreSQL
                                     ▲    │
 candidate browser ──HTTPS (cookie)──┘    │ room, tokens, dispatch (server API)
        │                                 ▼
        └──WebRTC (microphone only)──► LiveKit ◄──WebRTC── Tier-1 agent ──WSS──► OpenAI Realtime API
                                          │                    │                 (candidate audio, instructions,
                                          │ webhooks           │ segments,        approved questions; returns
                                          ▼                    │ telemetry        audio and transcripts)
                                    control plane ◄──HTTPS─────┘
                                    (internal API)
 optional, off by default:  LiveKit egress ──► S3-compatible object storage (candidate microphone, OGG)
 Tier-2 (ADR-022):          outbox ──► Redis stream ati:tier2:events (identifiers only) ──► Tier-2 worker
                              reads evidence (ati_tier2_evaluator) ──► LLM provider (text; none in this build)
                              ──► control plane internal API (attempts) ──► human review ──► report (staff only)
```

| # | Flow | Personal data | Protection in this build |
|---|---|---|---|
| F1 | Staff creates an interview | `candidate_ref`: an opaque external reference, which must not be a name or email by convention (not enforced) | Staff token (hashed at rest), tenant scoping, idempotency |
| F2 | Candidate exchanges the invitation, consents, starts, answers in text | Consent decisions; typed answers | Invitation token in the URL fragment (never logged); hashed tokens; `HttpOnly`, `SameSite=Strict` cookie; CSRF checks |
| F3 | Candidate audio to LiveKit and the agent | Voice (audio) | Media is relayed and never stored, unless F7 applies. The candidate token grants the microphone only. Self-hosted LiveKit: TLS (wss) is required in production; dev runs over ws on loopback |
| F4 | Agent to OpenAI | Candidate audio; interviewer instructions; approved questions; transcripts returned | Key held only by the agent; endpoint pinned (ADR-021); no provider data-retention setting is verified (see G4) |
| F5 | Agent to control plane | Candidate and interviewer transcript segments with timings; latency metrics; policy flag names | Per-session lease with fencing token; service token; https required in production |
| F6 | LiveKit to control plane (webhooks) | Participant identities (`candidate`, `agent-…`); room names (opaque) | Signature over the body with a dedicated key; body bounded before verification; 5-minute age bound; processed-event dedupe |
| F7 | Recording (opt-in, ADR-017) | Candidate microphone audio, written as a file | Tenant opt-in, candidate consent, attested storage encryption, explicit retention and voice mode are all required; only the candidate's microphone is recorded, and only while a question is open |
| F8 | Logs and metrics | None by design | Redacting log filter (tokens, keys, transcripts, extras); metric labels are routes, bounded methods and event names |
| F9 | Tier-2 evaluation (ADR-022) | Answer transcripts, questions and rubrics (text) to the evaluator, and with a model evaluator to an LLM provider; evaluator output (quotes of candidate speech) back to the control plane | Queue entries carry identifiers only; the evaluator role reads evidence and two narrow views, sees no candidate identity and writes nothing; attempts go through the internal API with a service token; synthetic test speech is never sent; every answer is reviewed by a person. Only the deterministic reference evaluator runs in this build: no provider is called. Model evaluators send `store: false` (OpenAI) and use pinned endpoints; provider terms are unverified (G4) |
| F10 | Review and report (ADR-022) | Reviewer scores and rationale; the report snapshot (scores, rationales, AI evidence quotes) | Staff-only, tenant-scoped, roles `reviewer`/`admin` decide and `admin`/`recruiter`/`reviewer` read reports; audit entries carry no rationale; candidates see no score |

## Inventory (PostgreSQL schema `ati`)

| Table | Content | Personal data | Written by | Read by | Retention | Deletion |
|---|---|---|---|---|---|---|
| `tenants`, `staff_tokens` | Tenant settings; staff token hashes, roles, labels | Staff token label (free text) | `ati-admin` | Control plane | Until revoked or expired | `ati-admin` (manual) |
| `candidates` | `external_ref` per tenant | Pseudonymous reference | Control plane | Control plane | Not defined | **None (G1)** |
| `interview_sessions` | State, plan, language, retention and recording policy snapshots | Linked to the candidate | Control plane | Control plane, Tier-1 views (limited columns) | Not defined | **None (G1)** |
| `invitations`, `candidate_sessions` | Token hashes, expiry, revocation | None (hashes only) | Control plane | Control plane | Until expiry | **None (G1)** |
| `consent_records` | Purpose-level decisions, notice version | Consent decisions | Control plane | Control plane | Not defined (consent evidence) | Append-only, **none (G1)** |
| `question_activations`, `candidate_answers`, `completion_requests`, `pause_intervals` | Interview timeline and answer metadata | Linked to the candidate | Control plane | Control plane, Tier-1 views (open question only) | Policy per session (`transcript_days`) | **None (G1)** |
| `transcript_segments` | What the candidate and the interviewer said or typed, with timings and provenance | **Candidate speech and text** | Control plane (from the agent and from text mode) | Control plane; Tier-1 sees only the open question's text inputs (ADR-018); the Tier-2 role (NOLOGIN) | Policy per session (`transcript_days`) | Append-only, **no purge job (G1)** |
| `session_events`, `integrity_events` | State transitions and integrity flags | Linked to the candidate | Control plane | Control plane | Policy per session (`session_events_days`) | Append-only, **no purge job (G1)** |
| `audit_logs` | Security-relevant actions and outcomes | Actor identifiers | Control plane, `ati-admin` | Operators | Not defined | Append-only by design |
| `outbox_events` | `answer.finalized`, `interview.completed`: identifiers only | None (identifiers) | Control plane | Outbox relay; Tier-2 role | Not defined | None |
| `rubrics` | Versioned rubrics: criteria, references, weights; synthetic demo rubrics carry indicator phrases | None | `ati-admin seed-plans` | Control plane, Tier-2 role | Immutable versions | None (immutable) |
| `evaluation_attempts` | Every evaluator response as received, its outcome and problems, provider, model, request id, tokens | **Quotes of candidate speech** in `raw_output` | Control plane (from the Tier-2 API) | Control plane | `retention_until` from the session's `assessment_output_days` | Append-only, **no purge job (G1)** |
| `evaluations` | Validated evaluations: scores, recomputed weighted score, cited evidence, routing reasons | **Quotes of candidate speech** in `evidence` | Control plane | Control plane, reviewers through the API | `retention_until` as above | Append-only, **no purge job (G1)** |
| `review_tasks`, `review_outcomes` | One task per answer; the reviewer's scores, rationale and the prior AI scores | Reviewer judgements about the answer; reviewer token id | Control plane | Reviewers and admins through the API | Not defined | Outcomes append-only, **none (G1)** |
| `reports` | The report snapshot written when the last review completes | Scores, rationales and AI evidence quotes | Control plane | Admins, recruiters and reviewers through the API | Not defined | Append-only, **none (G1)** |
| Redis `ati:tier2:events`, `ati:tier2:dlq` | Stream entries: event and session identifiers, the identifier-only payload | None (identifiers) | Outbox relay; Tier-2 worker (DLQ) | Tier-2 worker | Trimmed to about 100,000 (stream) and 10,000 (DLQ) entries | Trimming only (G7) |
| `idempotency_records` | Request hashes and stored responses for replays (credentials are never stored) | Response bodies (e.g. interview ids, `candidate_ref`) | Control plane | Control plane | 24 h by default | `ati-admin purge-operational` (audited) |
| `webhook_events` | LiveKit event ids and types | None | Control plane | Control plane | 24 h by default | `ati-admin purge-operational` (audited) |
| `agent_dispatches`, `agent_leases`, `session_usage` | Agent dispatch and lease records; provider usage counters | None | Control plane | Control plane | Not defined | None |
| `audio_recordings` (+ object storage) | Object key, status, timeline bounds; the audio file | **Candidate voice** (object) | Control plane, LiveKit egress | Operators | Explicit `audio_recording_days` (required) | `ati-admin recordings purge-expired` deletes the object and leaves an audited tombstone |

Roles (least privilege; ADR-016, ADR-020):
- `ati_control_plane` has no DELETE and no DDL. Append-only tables are protected by triggers.
- `ati_tier1_ctx` reads only the `tier1_*_v` views. These cover the active session's state, the open question and its text inputs.
- `ati_tier2_evaluator` can log in from Phase 2. It has SELECT on answers, segments, activations, questions, the outbox, rubrics and the views `tier2_session_v` (state, plan, language) and `tier2_evaluation_keys_v`, and nothing else: no candidates, notices, rooms or tokens, and no writes.

## Gaps (P0.3)

| ID | Gap | Consequence |
|---|---|---|
| G1 | Retention periods are recorded per session, but only recordings and operational records have purge procedures. No job deletes transcripts, timelines, consent records or candidates when their period ends. There is also no deletion-request workflow for a candidate. | Data is kept indefinitely. Production must not start until these exist; they are part of the `privacy_legal` gate. |
| G2 | No subject-access export, no appeal workflow, no contact route in the product. | Candidate rights cannot be exercised through the system. |
| G3 | Encryption in transit is enforced for the browser, LiveKit and agent paths in production (https and wss checks), and for the Tier-2 worker's database (`sslmode=verify-full`) and Redis (`rediss://`) connections. It is not enforced for the control plane's PostgreSQL connection (`sslmode` is not checked) or object storage. Encryption at rest is attested by configuration, not verified. | The deployment must prove both; the attestation flag is a tripwire, not evidence. |
| G4 | OpenAI's processing terms, data retention and region for audio and transcripts are not verified or configured. The same applies to the Tier-2 providers (OpenAI Responses with `store: false`, Anthropic Messages). | A data-processing agreement and retention settings are prerequisites of live use. |
| G5 | `candidate_ref` must not carry direct identifiers, but nothing enforces this. | A tenant could place emails or names in it. Validation or hashing is a design decision for the privacy review. |
| G6 | Logs are redacted by pattern, a second line of defence that can miss content. | Log retention and access control belong to the deployment. |
| G7 | The Redis streams are trimmed by length, not by age, and the DLQ is never drained automatically. Entries hold identifiers only. | Operators must watch the DLQ; a time-based trim is a deployment decision. |

## Verification

- Recording, purge and redaction behaviour: control-plane and agent tests (`internal/recording`, `cmd/ati-admin`, `tests/test_monitor_worker_logging.py`).
- Least-privilege grants: `internal/store` tests (including `TestTier2EvaluatorRoleReadsOnlyEvidenceAndNarrowViews`).
- Tier-2 flows: `internal/interview` and `internal/api` tests, the worker's tests, and `tests/e2e/test_tier2.py` (the log scan includes the worker).
- The gaps above are open: nothing in this repository closes them yet.
