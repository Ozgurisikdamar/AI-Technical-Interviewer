# Implementation status and repository audit

**Last updated:** 2026-11-03 (Phase 0 and Phase 1 milestones M1–M9, then a completion pass over every gap that could be tested here)
**Rule:** a capability counts as implemented only when its code, tests and integration are verified. Status labels:

| Label | Meaning |
|---|---|
| **Verified (local)** | Code and automated tests exist and pass in this repository (test references given) |
| **Verified (scripted, loopback)** | Passes end-to-end with the deterministic scripted voice engine over local WebRTC only; the production provider path is not exercised |
| **Implemented — unverified** | Code exists, but the integration cannot be verified in the current environment (blocker given) |
| **Not started** | No implementation yet (target phase given) |

All contracts and ADR-011 to ADR-020 are **Proposed**. The Phase 0 exit gate ("contracts reviewed") requires human architecture review, which automation cannot provide. Phase 1 work builds on Proposed contracts.

**Summary.** The Phase 1 voice MVP runs end to end in this repository with the deterministic scripted voice engine: a candidate opens an invitation in Chromium, consents per purpose, checks the microphone, and completes a voice or text interview against the real control plane, PostgreSQL, LiveKit OSS and the Tier-1 agent, with pause, refresh, disconnect, agent crash, provider failures (recognition, synthesis, model timeout, dropped stream), barge-in, staff revocation and withdrawal handled by the backend-enforced state machine. The completion pass applied per-question turn profiles, measured `low_energy`, aligned the latency metrics with BLUEPRINT §5, and fixed four defects found by new tests: credentials stored at rest for idempotent replays, a recording start that hung without an egress service, extra agent participants that were not removed, and a refused microphone explained as a generic failure. What is **not** verified: the OpenAI Realtime engine against the live provider (no credentials), real recording egress (no egress service), real networks (no TURN, loss or jitter emulation), container images (no Docker daemon), and every human gate (architecture, legal, benchmarks, production gates). Nothing here is cleared for real candidates (ADR-020).

## 1. Baseline audit (repository state before implementation)

The repository contained architecture documents only: `README.md`, `LICENSE` (Apache-2.0) and six files in `docs/architecture/` (about 1,500 lines). There were no source files, build files, tests, CI or infrastructure definitions.

- Functional implementation: 0%. MVP acceptance criteria met: 0/12. Production gates met: 0/5. Go-live checklist: 0/15.

## 2. Current status by milestone

| Milestone | Scope | Status |
|---|---|---|
| Phase 0 — contracts | State machine v1.1, schemas, prompts, content, fixtures, ADR-011–020, threat-model addendum, OpenAPI, test plan, hygiene checks, CI | **Verified (local)**: `make checks`, `make contracts-test` (120 tests). Human review pending |
| M2 — Go engine, config, logging | Pure flow engine from the embedded contract; ADR-020 config refusals; redacting logger; HTTP plumbing; architecture tests | **Verified (local)**: `make go-lint` (vet + staticcheck), `make go-test` (model-based walk over 3,000 seeds: 208 completions / 2,792 cancellations with every invariant checked; fuzzing; refusal matrix; log sentinels) |
| M3 — Schema, roles, transition service | Phase-1 schema with DB-enforced INV-1/INV-9, append-only triggers, least-privilege roles; transactional transition service (row lock + `state_version`, per-step events and audit, outbox with contract validation); ingestion with speech-time attribution; completion protocol; leases with fencing | **Verified (local)**: `make go-test` against PostgreSQL 16 as the real application role (privilege matrix, lifecycle, button/deadline paths, pause/resume, withdrawal, expiry, consent, attribution, concurrency storms, compromised-agent suite) |
| M4 — Auth, API, LiveKit, ingestion, completion | Two listeners; staff/candidate/agent authentication; idempotency keys (credentials never stored for replays); invitation rate limit; staff invitation revocation (pauses a live interview and deletes its room); LiveKit rooms, mic-only tokens, server-side dispatch with nonce-only metadata, claim verification, signed and deduplicated webhooks, removal of participants the control plane did not mint (including a second agent while one holds the lease); Tier-1 context via restricted views; metrics; sweepers; binaries | **Verified (local)** against PostgreSQL with a fake media layer: HTTP flow to completion, cross-candidate/tenant isolation, CSRF, listener separation, idempotency replay/reuse and concurrent creation, staff token expiry and revocation, invitation revocation, claim attacks and expired nonces, fencing, webhook trust (decoded dedicated-key signatures), decoded candidate token grants, rate limiting, security headers and environment marker on every response, telemetry to `/metrics`, outbox negatives, log sentinels, OpenAPI route parity, ADR-020 refusals isolated per gate, startup smoke tests of the binary, `ati-admin` commands. Real LiveKit wiring: see M7 |
| M5 — Recording | Fail-closed policy (deployment flag and encryption attestation, tenant opt-in, candidate consent, explicit retention, voice mode, open question, microphone track), `start_recording` on T05/T13, stop on pause/withdraw/complete and track unpublish, LiveKit TrackEgress client with bounded calls (an unanswered start fails closed) and an asynchronous start, egress webhooks, `audio_ref` on candidate segments, Tier-1 `recording_active` view, purge with audited tombstones (`ati-admin recordings purge-expired`), tenant policy command | **Verified (local)** with a fake egress and an in-process S3 server (gofakes3): policy matrix (each precondition alone, including no open question), lifecycle including the stop on completion (T11), audio refs, failures, unanswered starts, idempotent purge, webhook routing; mutation-checked. **Verified (scripted, loopback)** against the real LiveKit server without an egress service: the recording fails closed and the interview continues. **A real recording is unverified**: no egress service in this environment |
| M6 — Tier-1 agent | LiveKit worker on explicit dispatch; claim, lease and fenced writes over the internal API (no database access); interview controller; single parameterless tool with grace, pending-transcript wait and flushed manifest; answer windows tied to the prompt's own playout; prompt-delivery marking; per-question turn profiles; measured `low_energy`; BLUEPRINT §5 latency metrics from per-reply measurements; pause on provider errors, errored session close and undelivered prompts; scripted engine (fake VAD/STT/LLM/TTS inside `AgentSession`, pitch-coded intents and failure injection) and OpenAI Realtime engine; local-only turn handling (no hosted LiveKit Inference); redacting JSON logs | **Verified (local)**: `make agent-test` (ruff, mypy strict, 176 pytest tests incl. mutation-checked properties). OpenAI engine: configuration and turn-profile updates unit-tested only, **not verified against the provider** (no credentials) |
| M7 — Media E2E | `tools/dev/stack.sh` (PostgreSQL, checksum-verified LiveKit OSS 1.13.11, control plane, agent); synthetic candidate over real WebRTC (`tests/e2e`); missing-agent sweeper, resume re-dispatch, room deletion after the closing remark | **Verified (scripted, loopback)**: `make e2e`, 8/8 (full plan, barge-in, disconnect/resume, pause intake, injection, provider failure, button path, agent crash) plus post-run log and audio-file scans. Barge-in stop latency at the candidate, two runs of n=5: p50 406 and 408 ms, max 417 ms (loopback, uncalibrated 200 ms minimum interruption) |
| M8 — Web UI | Next.js 16 / React 19 candidate UI (`apps/web`): fragment-only invitation exchanged on click, purpose-level DRAFT consent, device check, live room (mic-only publishing, unpublished while paused, captions from interviewer transcription, ARIA live status), pause/resume/finish/withdraw from `allowed_actions`, refresh rejoin, device-loss pause; per-request CSP nonce, strict security headers, lint guardrails, bundle secret scan | **Verified (local)**: `make web-test` (eslint, tsc, 222 unit tests, production build, bundle scan). **Verified (scripted, loopback)** in Chromium against the real stack: voice flow to completion and keyboard/device-loss/withdrawal flow (`tests/e2e/test_browser.py`). Text answers wait for M9 |
| M9 — Text mode | `POST /text-responses` (text mode only, activation-bound, refused during a pending completion; control-plane-written `candidate_text` segments; audit without text); Tier-1 view of the open question's text answers; agent passes each once as user input; text-mode greeting; UI offers text mode without a microphone | **Verified (local)**: G API tests (binding, mode, length, ordering, contract-valid context, manifest-free confirmation, no text in logs), A tests (user input only, once, other activations ignored). **Verified (scripted, loopback)** in Chromium with the microphone refused (`text_flow.mjs`): captions, no published track, completion |

## 3. Gap analysis (requirement → status)

| ID | Requirement (source) | Status | Evidence / target |
|---|---|---|---|
| G1 | Tier-1/Tier-2 isolation (ADR-001, BP §3.1) | **Verified (local)**: separate deployables and lockfiles; the agent has no database driver or credentials (dependency test, import boundaries); Tier-1 reads only allowlisted views through `ati_tier1_ctx`; evaluator role NOLOGIN with no live-table writes; ID-only outbox payloads; compromised-agent suite | Tier-2 workers Phase 2 |
| G2 | Modular control plane, contract tests (ADR-002) | **Verified (local)**: package boundaries (`internal/archtest`), embedded-contract drift test, OpenAPI route parity | — |
| G3 | LiveKit/WebRTC media (ADR-003) | **Verified (scripted, loopback)** (`make e2e`) | TURN, loss and jitter Blocked (no netem) |
| G4 | App-owned provider adapter (ADR-004) | `VoiceEngine` with scripted and OpenAI Realtime engines; scripted **Verified (local + E2E)** including per-question turn profiles; OpenAI **Implemented — unverified** (turn-profile updates unit-tested on the model) | Live provider blocked: no credentials |
| G5 | PostgreSQL canonical; Qdrant projection (ADR-005) | PostgreSQL **Verified (local)** | `internal/store`, `internal/interview`; Qdrant Phase 2 |
| G6 | Durable jobs, DLQ (ADR-006) | Transactional outbox **Verified (local)** (contract-validated payloads, insertion order) | Relay/consumers/DLQ Phase 2 |
| G7 | Evidence before score (ADR-007) | Contract **Verified (local)** (amended schema, fixtures) | Validator Phase 2 |
| G8 | Human accountability (ADR-008) | Contract **Verified (local)** (INV-6, no decision fields) | Workflow Phase 5 |
| G9 | Privacy by design (ADR-009) | **Verified (local)**: purpose-level DRAFT notices; recording optional, fail-closed, retention-bound with audited purge; PII-minimal schema; redacted logs; E2E log and audio-file scans | Access, export and deletion workflows Phase 5; legal review open; real egress unverified |
| G10 | No cheating inference (ADR-010) | Contract **Verified (local)** (no timing in handoff events) | — |
| G11 | State machine + invariants (BP §4) | **Verified (local)**: contract, pure engine (model-based walk, fuzzing), persisted transitions with per-step events and audit, database-enforced invariants | — |
| G12 | Latency metrics (BP §5, §16) | **Verified (scripted, loopback)**: TTFB, TTFA, end of turn (candidate turn end to first reply audio) and full response from the framework's per-reply measurements, plus barge-in stop latency, reach `/metrics` (E2E); candidate-side barge-in measured in E2E | Loopback and scripted only; no provider or network figures |
| G13 | Barge-in, turn policy (BP §6) | **Verified (scripted, loopback)**: barge-in stops playback without closing the answer; received-silence grace; frame starvation never ends a turn; each question's turn profile (`extended_pause` waits longer) | Thresholds uncalibrated (Phase 6) |
| G14 | Rubric and weights (BP §7) | Contract **Verified (local)** (weights consistent across documents) | Phase 2 |
| G15 | Tier-1 prompt and runtime context (BP §8, P&E §1) | **Verified (local)**: pinned prompt checked at startup; allowlisted context validated before use; candidate text never enters instructions | Live model behaviour unverified |
| G16 | Tier-2 prompt (P&E §2) | Contract **Verified (local)** (verbatim + sha256) | Phase 2 |
| G17 | Adversarial controls (BP §9) | Backend **Verified (local)**; scripted injection and forbidden tool call over real media change nothing; server-enforced media grants, intruder removal, forged webhooks and deleted rooms over real LiveKit (**Verified (scripted, loopback)**) | Live-model injection suite blocked (no credentials) |
| G18 | Coding sandbox, whiteboard (BP §10) | Not started | Phase 4 |
| G19 | HITL routing, override audit (BP §11) | Integrity flags for future routing **Verified (local)** (missing segments, echo, prompt deviation, unassigned and late speech, synthetic source); measured `low_energy` and output-monitor policy flags **Verified (scripted, loopback)** | Routing and review workflow Phases 2 and 5; thresholds uncalibrated |
| G20 | Data entities, transcript immutability (BP §13) | **Verified (local)** | Append-only grants + triggers tested even for the owner; answers bind exact segment IDs |
| G21 | API surface (BP §17) | **Verified (local)** for every Phase-1 endpoint, including text responses | Evaluations, report, appeals, reviews: Phases 2 and 5 |
| G22 | Observability (BP §16) | Prometheus metrics (HTTP, transitions, voice latency, webhooks, sweepers, policy flags); correlation IDs; redacted JSON logs in every process, checked by the E2E log scan | Dashboards and OTel export Phase 6 |
| G23 | QA/chaos matrix (BP §18) | Plan **Verified (local)** ([TEST_PLAN.md](../qa/TEST_PLAN.md)) | Per test plan |
| G24 | Text/non-audio fallback (TM §4–5) | **Verified (local; browser: scripted, loopback)**: text response mode end to end | A fully text interviewer (no audio output) and a broader accommodations process are Phase 5–6 |
| G25 | Threat model for new boundaries | Document written ([addendum](../architecture/THREAT_MODEL_ADDENDUM_PHASE1.md)) | Human review pending |
| G26 | Cost model usage capture (STACK §5) | Per-session usage recorded (**Verified (scripted, loopback)**: engine and audio durations) | Provider token counts need the live engine |
| G27 | Repository conventions | **Verified (local)** | `tools/checks/*`, ADR immutability pin test |
| G28 | MVP acceptance criteria and production gates | Phase-1 criteria 1–4 and 10–12 verified (1–3 with the scripted engine, loopback only); 5–9 belong to Phases 2 and 5; production gates 0/5 | See section 4 |

## 4. MVP acceptance criteria (DECISIONS_AND_ROADMAP)

| # | Criterion | Status |
|---|---|---|
| 1 | Consented voice interview in a supported browser | **Verified (scripted, loopback)** in Chromium against the real stack (M8) and with a synthetic WebRTC participant (M7); live provider unverified |
| 2 | State survives reconnect without duplicates or corruption | **Verified (scripted, loopback)**: disconnect, agent crash and provider failure each pause and resume as a new activation; one answer per item (G concurrency suites + E) |
| 3 | Interruption stops playback and preserves state | **Verified (scripted, loopback)** (barge-in E2E) |
| 4 | Transcript persisted with speaker, timestamps, sequence, version | **Verified (local)** (G ingestion tests; E2E data checks) |
| 5 | Evaluation asynchronous, never blocks voice | Phase 2 (handoff contract Verified) |
| 6 | Every claim links to transcript evidence and rubric criteria | Phase 2 (schema Verified) |
| 7 | Backend validates schema, ranges, evidence, arithmetic | Phase 2 |
| 8 | Low-confidence or failed-integrity evaluation → human review | Phase 2 |
| 9 | Candidate report and appeal path | Phase 5 |
| 10 | No secrets, raw audio or sensitive data in logs | **Verified (local)**: G and A sentinel tests; E2E scans every process log for credentials, JWTs and transcript text and the file system for audio files |
| 11 | Tests for injection, duplicates, retry, provider failure, authorization | **Verified (local)** for the backend and agent; scripted injection and five kinds of provider failure over real media; live-model injection blocked |
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
| Media path in container | Spike first, then `tools/dev/stack.sh` and `tests/e2e` | Explicit dispatch, two-way audio, refresh/rejoin and signed webhooks run in every `make e2e` |
| Agent worker proxy | `AgentServer` inherits `HTTPS_PROXY` and ignores `NO_PROXY` | Pass `http_proxy` explicitly from configuration |
| Object storage / egress | MinIO server binaries discontinued (HTTP 410); egress needs GStreamer and Docker | minio-go against an in-process S3 server (gofakes3); real egress unverified |
| OpenAI / LiveKit Cloud | Network reachable; no credentials in the environment | Live paths unverified |
| Docker | CLI and compose present; no daemon | Images and compose deferred |
| Browser | Playwright 1.63 with Chromium preinstalled | Reuse |
| Network emulation | No `tc` / `ip` | Packet-loss, jitter and TURN tests blocked |
| Linters | golangci-lint 2.5 (built with Go 1.25) rejects Go 1.26 modules | `gofmt` + `go vet` + staticcheck v0.8.2 (`make go-lint`); ruff + mypy strict (agent); eslint + tsc (web) |
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
| GitHub Actions jobs are never assigned a runner | CI results (`ci.yml` defines checks, contracts, control-plane, Tier-1 agent and web jobs) | Repository or account Actions settings and billing; until then `make test` and `make e2e` are the evidence |

## 9. How to verify

| Command | What it runs | Result at this revision |
|---|---|---|
| `make checks` | repository hygiene (secrets, media files, PII patterns), Python import boundaries, generated state diagram | pass |
| `make contracts-test` | schema meta-validation and fixtures, state-machine structure, doc drift, isolation, OpenAPI | 120 passed |
| `make workflow-lint` | actionlint v1.7.7 on `.github/workflows/ci.yml` | pass |
| `make go-lint` / `make go-test` | gofmt, vet, staticcheck; Go tests with the race detector against PostgreSQL 16 | pass (15 packages with tests) |
| `make agent-test` | ruff, mypy (strict), pytest for the Tier-1 agent | 176 passed |
| `make web-test` | eslint, tsc, vitest, production build, bundle secret scan | 229 passed; build and scan pass |
| `make e2e` | local stack; synthetic WebRTC candidate (21 tests: voice interview, failure injection and signals, recording, media security) and Chromium flows (7 tests, each screen audited with axe-core); log and audio-file scans | every test passed on the running stack; the result of a full run from a fresh stack is recorded at the next revision |

## 10. Known gaps and follow-ups (Phase 1 scope)

- The OpenAI Realtime engine is configuration-tested only (including its turn-profile updates); a recorded live run is part of the Phase 1 exit criteria.
- Interruption, grace, dispatch-timeout, endpointing (0.3 s / 1.2 s), `low_energy` (-35 dBFS), egress-timeout (10 s) and semantic-VAD eagerness values are uncalibrated placeholders (barge-in stop latency at the candidate: p50 about 0.4 s on loopback, dominated by the 200 ms minimum interruption duration).
- Blocked autoplay (the enable-audio control) is unit-tested only: automated Chromium grants a user activation to every scripted evaluation, so it cannot be reproduced end to end.
- Data packets from the candidate are dropped by LiveKit; the end-to-end test asserts the server-side permission, because a drop cannot be observed without a second legitimate subscriber.
- An agent-kind token minted with a leaked API key while no lease is held cannot be told apart from the dispatched replacement until it claims (claiming needs the single-use nonce); this is the documented trusted-infrastructure residual risk.
- No screen-reader run; keyboard, ARIA wiring and automated WCAG 2.1 A/AA checks (axe-core) are browser-tested.
- Container images and deployment manifests are not built (no Docker daemon).
