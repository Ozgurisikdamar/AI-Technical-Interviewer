# Implementation status and repository audit

**Last updated:** 2026-11-03 (Phase 0 milestone)
**Rule:** a capability counts as implemented only when its code, tests and integration are verified. Status labels:

| Label | Meaning |
|---|---|
| **Verified (local)** | Code and automated tests exist and pass in this repository (test references given) |
| **Verified (scripted, loopback)** | Passes end-to-end with the deterministic scripted voice engine over local WebRTC only; the production provider path is not exercised |
| **Implemented — unverified** | Code exists, but the integration cannot be verified in the current environment (blocker given) |
| **Not started** | No implementation yet (target phase given) |

All contracts and ADR-011 to ADR-020 are **Proposed**. The Phase 0 exit gate ("contracts reviewed") requires human architecture review, which automation cannot provide. Phase 1 work builds on Proposed contracts.

## 1. Baseline audit (repository state before implementation)

The repository contained architecture documents only: `README.md`, `LICENSE` (Apache-2.0) and six files in `docs/architecture/` (about 1,500 lines). There were no source files, build files, tests, CI or infrastructure definitions.

- Functional implementation: 0%. MVP acceptance criteria met: 0/12. Production gates met: 0/5. Go-live checklist: 0/15.

## 2. Current status by milestone

| Milestone | Scope | Status |
|---|---|---|
| Phase 0 — contracts | State machine v1.1, schemas, prompts, content, fixtures, ADR-011–020, threat-model addendum, OpenAPI, test plan, hygiene checks, CI | **Verified (local)**: `make checks`, `make contracts-test` (117 tests). Human review pending |
| M2 — Go engine, config, logging | Pure flow engine from the embedded contract; ADR-020 config refusals; redacting logger; HTTP plumbing; architecture tests | **Verified (local)**: `make go-lint` (vet + staticcheck), `make go-test` (model-based walk over 3,000 seeds: 208 completions / 2,792 cancellations with every invariant checked; fuzzing; refusal matrix; log sentinels) |
| M3 — Schema, roles, transition service | Phase-1 schema with DB-enforced INV-1/INV-9, append-only triggers, least-privilege roles; transactional transition service (row lock + `state_version`, per-step events and audit, outbox with contract validation); ingestion with speech-time attribution; completion protocol; leases with fencing | **Verified (local)**: `make go-test` against PostgreSQL 16 as the real application role (privilege matrix, lifecycle, button/deadline paths, pause/resume, withdrawal, expiry, consent, attribution, concurrency storms, compromised-agent suite) |
| M4 — Auth, API, LiveKit, ingestion, completion | Two listeners; staff/candidate/agent authentication; idempotency keys; invitation rate limit; LiveKit rooms, mic-only tokens, server-side dispatch with nonce-only metadata, claim verification, signed and deduplicated webhooks; Tier-1 context via restricted views; metrics; sweepers; binaries | **Verified (local)** against PostgreSQL with a fake media layer: HTTP flow to completion, cross-candidate/tenant isolation, CSRF, listener separation, idempotency replay/reuse, claim attacks, fencing, webhook trust, rate limiting, log sentinels, OpenAPI route parity. Real LiveKit wiring: see M7 |
| M5 — Recording | | Not started |
| M6 — Tier-1 agent | LiveKit worker on explicit dispatch; claim, lease and fenced writes over the internal API (no database access); interview controller; single parameterless tool with grace, pending-transcript wait and flushed manifest; answer windows tied to the prompt's own playout; prompt-delivery marking; scripted engine (fake VAD/STT/LLM/TTS inside `AgentSession`, pitch-coded intents) and OpenAI Realtime engine; local-only turn handling (no hosted LiveKit Inference); redacting JSON logs | **Verified (local)**: `make agent-test` (ruff, mypy strict, 139 pytest tests incl. mutation-checked properties). OpenAI engine: configuration unit-tested only, **not verified against the provider** (no credentials) |
| M7 — Media E2E | `tools/dev/stack.sh` (PostgreSQL, checksum-verified LiveKit OSS 1.13.11, control plane, agent); synthetic candidate over real WebRTC (`tests/e2e`); missing-agent sweeper, resume re-dispatch, room deletion after the closing remark | **Verified — scripted engine, loopback only**: `make e2e`, 8/8 (full plan, barge-in, disconnect/resume, pause intake, injection, provider failure, button path, agent crash) plus post-run log and audio-file scans. Barge-in stop latency at the candidate: n=5, p50 406 ms, max 417 ms (loopback, uncalibrated 200 ms interruption threshold) |
| M8 — Web UI | | Not started |
| M9 — Text mode | | Not started |

## 3. Gap analysis (requirement → status)

| ID | Requirement (source) | Status | Evidence / target |
|---|---|---|---|
| G1 | Tier-1/Tier-2 isolation (ADR-001, BP §3.1) | Contract level **Verified (local)**; runtime Not started | `contracts/tests/test_isolation_contracts.py`, import-boundary check; runtime controls M3–M6 |
| G2 | Modular control plane, contract tests (ADR-002) | Partial: module boundaries **Verified (local)** | `internal/archtest`; API M4 |
| G3 | LiveKit/WebRTC media (ADR-003) | **Verified — scripted engine, loopback only** (`make e2e`) | TURN, loss and jitter Blocked (no netem) |
| G4 | App-owned provider adapter (ADR-004) | `VoiceEngine` with scripted and OpenAI Realtime engines; scripted **Verified (local + E2E)**; OpenAI **Implemented — unverified** | Live provider blocked: no credentials. Per-question turn profiles are passed to the agent but not yet applied to the provider's turn detection |
| G5 | PostgreSQL canonical; Qdrant projection (ADR-005) | PostgreSQL **Verified (local)** | `internal/store`, `internal/interview`; Qdrant Phase 2 |
| G6 | Durable jobs, DLQ (ADR-006) | Transactional outbox **Verified (local)** (contract-validated payloads, insertion order) | Relay/consumers/DLQ Phase 2 |
| G7 | Evidence before score (ADR-007) | Contract **Verified (local)** (amended schema, fixtures) | Validator Phase 2 |
| G8 | Human accountability (ADR-008) | Contract **Verified (local)** (INV-6, no decision fields) | Workflow Phase 5 |
| G9 | Privacy by design (ADR-009) | Content **Verified (local)** (purpose-level DRAFT notices, recording optional) | M3–M5 |
| G10 | No cheating inference (ADR-010) | Contract **Verified (local)** (no timing in handoff events) | — |
| G11 | State machine + invariants (BP §4) | Contract and engine **Verified (local)** | `internal/flow` model-based + fuzz tests; persistence M3 |
| G12 | Latency metrics (BP §5, §16) | Agent histograms (TTFB, TTFA, end of turn, full response, barge-in) reported to Prometheus; candidate-side barge-in measured in E2E | Loopback and scripted only; no provider or network figures |
| G13 | Barge-in, turn policy (BP §6) | **Verified — scripted engine, loopback only**: barge-in stops playback without closing the answer; received-silence grace; frame starvation never ends a turn | Thresholds uncalibrated (Phase 6) |
| G14 | Rubric and weights (BP §7) | Contract **Verified (local)** (weights consistent across documents) | Phase 2 |
| G15 | Tier-1 prompt and runtime context (BP §8, P&E §1) | **Verified (local)**: pinned prompt checked at startup; allowlisted context validated before use; candidate text never enters instructions | Live model behaviour unverified |
| G16 | Tier-2 prompt (P&E §2) | Contract **Verified (local)** (verbatim + sha256) | Phase 2 |
| G17 | Adversarial controls (BP §9) | Backend **Verified (local)**; scripted injection and forbidden tool call over real media change nothing (**Verified — scripted, loopback**) | Live-model injection suite blocked (no credentials) |
| G18 | Coding sandbox, whiteboard (BP §10) | Not started | Phase 4 |
| G19 | HITL routing, override audit (BP §11) | Not started | Phase 2, 5 |
| G20 | Data entities, transcript immutability (BP §13) | **Verified (local)** | Append-only grants + triggers tested even for the owner; answers bind exact segment IDs |
| G21 | API surface (BP §17) | **Verified (local)** for implemented endpoints | `internal/api` tests; text responses return 501 until M9 |
| G22 | Observability (BP §16) | Prometheus metrics (HTTP, transitions, voice latency, webhooks, sweepers, policy flags); correlation IDs; redacted JSON logs in every process, checked by the E2E log scan | Dashboards and OTel export Phase 6 |
| G23 | QA/chaos matrix (BP §18) | Plan **Verified (local)** ([TEST_PLAN.md](../qa/TEST_PLAN.md)) | Per test plan |
| G24 | Text/non-audio fallback (TM §4–5) | Contract **Verified (local)** | M9 |
| G25 | Threat model for new boundaries | Document written ([addendum](../architecture/THREAT_MODEL_ADDENDUM_PHASE1.md)) | Human review pending |
| G26 | Cost model usage capture (STACK §5) | Per-session usage recorded (**Verified — scripted**: engine and audio durations) | Provider token counts need the live engine |
| G27 | Repository conventions | **Verified (local)** | `tools/checks/*`, ADR immutability pin test |
| G28 | MVP acceptance criteria and production gates | 0/12, 0/5 | See section 4 |

## 4. MVP acceptance criteria (DECISIONS_AND_ROADMAP)

| # | Criterion | Status |
|---|---|---|
| 1 | Consented voice interview in a supported browser | Voice interview over real WebRTC **Verified — scripted, loopback** (synthetic participant, M7); browser path M8 |
| 2 | State survives reconnect without duplicates or corruption | **Verified — scripted, loopback**: disconnect, agent crash and provider failure each pause and resume as a new activation; one answer per item (G concurrency suites + E) |
| 3 | Interruption stops playback and preserves state | **Verified — scripted, loopback** (barge-in E2E) |
| 4 | Transcript persisted with speaker, timestamps, sequence, version | **Verified (local)** (G ingestion tests; E2E data checks) |
| 5 | Evaluation asynchronous, never blocks voice | Phase 2 (handoff contract Verified) |
| 6 | Every claim links to transcript evidence and rubric criteria | Phase 2 (schema Verified) |
| 7 | Backend validates schema, ranges, evidence, arithmetic | Phase 2 |
| 8 | Low-confidence or failed-integrity evaluation → human review | Phase 2 |
| 9 | Candidate report and appeal path | Phase 5 |
| 10 | No secrets, raw audio or sensitive data in logs | **Verified (local)**: G and A sentinel tests; E2E scans every process log for credentials, JWTs and transcript text and the file system for audio files |
| 11 | Tests for injection, duplicates, retry, provider failure, authorization | **Verified (local)** for the backend and agent; scripted injection and provider failure over real media; live-model injection blocked |
| 12 | No automatic adverse decision from AI or telemetry | Structural: no decision fields or endpoints (Verified, C) |

## 5. Dependency and infrastructure assessment

| Item | Finding | Decision |
|---|---|---|
| Go | Local 1.24.7; pgx 5.12, LiveKit server-sdk-go 2.19 and protocol 1.53 require Go ≥ 1.26 | `go 1.26` with toolchain auto-download (verified reachable) |
| LiveKit Go APIs | `CreateDispatch`, `CreateRoom{DepartureTimeout, MaxParticipants}`, `CanPublishSources`, `CanPublishData`, `webhook.ReceiveWebhookEvent` present | Use |
| Python | 3.13 with uv; livekit-agents 1.8.10 (`AgentServer`, `AgentSession`, `RoomOptions`), OpenAI realtime engine (`gpt-realtime`, `semantic_vad`, `gpt-4o-transcribe`) | Separate agent lockfile |
| Node | 22.23 with pnpm; Next 16.4, React 19.3, livekit-client 2.23 | Pin TypeScript 6.0.x (not the native TS 7 compiler) |
| PostgreSQL | 16.15 server binaries (`/usr/lib/postgresql/16/bin`, refuse to run as root) | Ephemeral cluster via `runuser -u postgres` in a postgres-owned temp directory |
| LiveKit server | OSS v1.13.11 release (sha256 `1e9ee90c…43fb`) | Local dev server: `rtc.node_ip` set to the container address, `room.auto_create: false`, dedicated webhook key |
| Media path in container | Verified by a throwaway spike: explicit dispatch with metadata, two-way audio between Python rtc clients and Chromium, refresh/rejoin, signed webhooks | Basis for E2E (M7) |
| Agent worker proxy | `AgentServer` inherits `HTTPS_PROXY` and ignores `NO_PROXY` | Pass `http_proxy` explicitly from configuration |
| Object storage / egress | MinIO server binaries discontinued (HTTP 410); egress needs GStreamer and Docker | minio-go against an in-process S3 server (gofakes3); real egress unverified |
| OpenAI / LiveKit Cloud | Network reachable; no credentials in the environment | Live paths unverified |
| Docker | CLI and compose present; no daemon | Images and compose deferred |
| Browser | Playwright 1.63 with Chromium preinstalled | Reuse |
| Network emulation | No `tc` / `ip` | Packet-loss, jitter and TURN tests blocked |
| Linters | golangci-lint 2.5 built with Go 1.25 | Fall back to `go vet` / staticcheck if it rejects Go 1.26 modules |
| Unrelated credentials in the environment | Present (other projects' databases and services) | Never used; tests use ephemeral local services only |

## 6. Prioritized implementation plan (relative sizing; no calendar estimates without team-capacity data, BP §19)

| Phase | Exit acceptance criteria |
|---|---|
| 0 Contracts | Machine-readable contracts with drift, fixture and isolation tests green; human review of contracts and ADR-011–020 |
| 1 Voice MVP | Section 4 criteria 1–4 and 10–12 verified (scripted engine, loopback), live-provider run recorded, interruption and reconnect tests green |
| 2 Evaluation | Outbox relay to Redis Streams (ack/retry/DLQ/reclaim); evaluator deployable with its own DB role and provider key; deterministic validator covering every P&E §8 test; transcript-version binding; review routing; Qdrant with canonical fallback; human-labelled benchmark; resolve C3/C4 |
| 3 Interview intelligence | Isolated CV/JD parsing; versioned role/seniority plans; follow-up policy decided (C5) |
| 4 Coding and design | Sandbox passes the TM §5 sandbox matrix; Monaco; collaborative canvas |
| 5 Reports and review | Reviewer RBAC, override audit, candidate report and appeal, recording access with audit, access/export/deletion |
| 6 Hardening | All five production gates and the go-live checklist; latency by region, device and network; chaos; cost model |

## 7. Risks and unresolved conflicts

| ID | Item | Status |
|---|---|---|
| C1 | Pause only from QUESTION_ACTIVE; no withdrawal or expiry | Resolved by ADR-012 (owner-approved direction) |
| C2 | `evidence_honesty` naming; no quote or segment references | Resolved by ADR-013 (owner-approved direction) |
| C3 | `hallucination_risk` semantics undefined | Open: recommend evaluator-output risk only (Phase 2) |
| C4 | `evaluation_version` compared to rubric version; no model, prompt or transcript versions | Open: keep in the DB envelope (Phase 2) |
| C5 | Tier-2 `recommendation: FOLLOW_UP` vs "Tier-2 cannot steer the interview" | Open: advisory only; live follow-ups come only from the plan; decide before Phase 3 |
| C6 | API surface missing consent, invitation, agent and webhook endpoints | Resolved by ADR-016 |
| C7 | Evidence transcript produced inside the Tier-1 provider session | Accepted MVP risk (ADR-014) |
| C8 | Consent gate vs lawful basis | Open (legal): purpose-level records; every basis is `to_be_determined_by_legal` |
| C9 | Append-only evidence vs erasure | Open: audited purge for recordings (ADR-017); general deletion Phase 5 |
| C10 | Diagram `Context → Qdrant` arrow could leak rubric data into Tier-1 | Resolved by design: the Tier-1 context has no rubric or Qdrant access |
| R1–R9 | Technical risks | See [threat-model addendum](../architecture/THREAT_MODEL_ADDENDUM_PHASE1.md) §3 and the ADR consequence sections |

## 8. Blockers

| Blocker | Affects | Unblock |
|---|---|---|
| No OpenAI or LiveKit Cloud credentials | Live voice path, live-model injection suite, provider latency, usage numbers | Provide `OPENAI_API_KEY` (and optionally `LIVEKIT_URL`, `LIVEKIT_API_KEY`, `LIVEKIT_API_SECRET`) as environment secrets |
| No Docker daemon | Container images, compose | CI or a Docker-enabled environment |
| No egress service | Real recording | Egress deployment (Docker) |
| No `tc` / netem | Packet loss, jitter, TURN chaos | Lab or CI runner with NET_ADMIN |
| Human review required | Phase 0 gate, legal notice approval, benchmarks, production gates | Architecture, legal and qualified reviewers |
