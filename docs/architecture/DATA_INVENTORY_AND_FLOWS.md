# Data inventory and data flows

**Status:** Draft engineering record, written 2026-11-04 from the code. It is not a legal assessment. The
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
 Phase 2 (planned):         outbox ──► Redis Streams (identifiers only) ──► Tier-2 evaluator ──► LLM provider (text)
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
| F9 | Phase 2: Tier-2 evaluation (planned) | Answer transcripts (text) to an LLM provider | Not built. The design requires: identifiers only on the queue; a read-only evaluator role with no access to candidate identity; human review of every evaluation |

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
| `outbox_events` | `answer.finalized`, `interview.completed`: identifiers only | None (identifiers) | Control plane | Phase 2 relay | Not defined | None |
| `idempotency_records` | Request hashes and stored responses for replays (credentials are never stored) | Response bodies (e.g. interview ids, `candidate_ref`) | Control plane | Control plane | 24 h by default | `ati-admin purge-operational` (audited) |
| `webhook_events` | LiveKit event ids and types | None | Control plane | Control plane | 24 h by default | `ati-admin purge-operational` (audited) |
| `agent_dispatches`, `agent_leases`, `session_usage` | Agent dispatch and lease records; provider usage counters | None | Control plane | Control plane | Not defined | None |
| `audio_recordings` (+ object storage) | Object key, status, timeline bounds; the audio file | **Candidate voice** (object) | Control plane, LiveKit egress | Operators | Explicit `audio_recording_days` (required) | `ati-admin recordings purge-expired` deletes the object and leaves an audited tombstone |

Roles (least privilege; ADR-016, ADR-020):
- `ati_control_plane` has no DELETE and no DDL. Append-only tables are protected by triggers.
- `ati_tier1_ctx` reads only the `tier1_*_v` views. These cover the active session's state, the open question and its text inputs.
- `ati_tier2_evaluator` stays NOLOGIN until Phase 2.

## Gaps (P0.3)

| ID | Gap | Consequence |
|---|---|---|
| G1 | Retention periods are recorded per session, but only recordings and operational records have purge procedures. No job deletes transcripts, timelines, consent records or candidates when their period ends. There is also no deletion-request workflow for a candidate. | Data is kept indefinitely. Production must not start until these exist; they are part of the `privacy_legal` gate. |
| G2 | No subject-access export, no appeal workflow, no contact route in the product. | Candidate rights cannot be exercised through the system. |
| G3 | Encryption in transit is enforced for the browser, LiveKit and agent paths in production (https and wss checks). It is not enforced for PostgreSQL (`sslmode` is not checked) or object storage. Encryption at rest is attested by configuration, not verified. | The deployment must prove both; the attestation flag is a tripwire, not evidence. |
| G4 | OpenAI's processing terms, data retention and region for audio and transcripts are not verified or configured. The same applies to the planned Tier-2 provider. | A data-processing agreement and retention settings are prerequisites of live use. |
| G5 | `candidate_ref` must not carry direct identifiers, but nothing enforces this. | A tenant could place emails or names in it. Validation or hashing is a design decision for the privacy review. |
| G6 | Logs are redacted by pattern, a second line of defence that can miss content. | Log retention and access control belong to the deployment. |

## Verification

- Recording, purge and redaction behaviour: control-plane and agent tests (`internal/recording`, `cmd/ati-admin`, `tests/test_monitor_worker_logging.py`).
- Least-privilege grants: `internal/store` tests.
- The gaps above are open: nothing in this repository closes them yet.
