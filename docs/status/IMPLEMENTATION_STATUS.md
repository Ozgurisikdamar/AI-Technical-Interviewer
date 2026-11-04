# Implementation status and repository audit

**Last updated:** 2026-11-04 (Milestone C: data and ML lifecycle, offline benchmark harness; Milestone B: Tier-2 evaluation MVP, ADR-022; earlier the same day, Milestone A: Phase 1 audit closure, see [PHASE1_AUDIT.md](../qa/PHASE1_AUDIT.md); 2026-11-03: Phase 0 and Phase 1 milestones M1–M9 and a completion pass)
**Rule:** a capability counts as implemented only when its code, tests and integration are verified. Status labels:

| Label | Meaning |
|---|---|
| **Verified (local)** | Code and automated tests exist and pass in this repository (test references given) |
| **Verified (scripted, loopback)** | Passes end-to-end with the deterministic scripted voice engine over local WebRTC only; the production provider path is not exercised |
| **Implemented — unverified** | Code exists, but the integration cannot be verified in the current environment (blocker given) |
| **Not started** | No implementation yet (target phase given) |

All contracts and ADR-011 to ADR-022 are **Proposed**. The Phase 0 exit gate ("contracts reviewed") requires human architecture review, which automation cannot provide. Phase 1 work builds on Proposed contracts.

**Summary.** The Phase 1 voice MVP runs end to end in this repository with the deterministic scripted voice engine: a candidate opens an invitation in Chromium, consents per purpose, checks the microphone, and completes a voice or text interview against the real control plane, PostgreSQL, LiveKit OSS and the Tier-1 agent, with pause, refresh, disconnect, agent crash, provider failures (recognition, synthesis, model timeout, dropped stream), barge-in, staff revocation and withdrawal handled by the backend-enforced state machine. The completion pass applied per-question turn profiles, measured `low_energy`, aligned the latency metrics with BLUEPRINT §5, and fixed four defects found by new tests: credentials stored at rest for idempotent replays, a recording start that hung without an egress service, extra agent participants that were not removed, and a refused microphone explained as a generic failure. What is **not** verified: the OpenAI Realtime engine against the live provider (no credentials), real recording egress (no egress service), real networks (no TURN, loss or jitter emulation), container images (no Docker daemon), and every human gate (architecture, legal, benchmarks, production gates). Nothing here is cleared for real candidates (ADR-020).

**Milestone A (2026-11-04).** An audit of the handover findings against the code confirmed 15 control-plane defects and 3 Tier-1 defects, each now fixed with a regression test (11 of them reproduced on the old code; [PHASE1_AUDIT.md](../qa/PHASE1_AUDIT.md)). The most serious:
- an unbounded webhook body read before signature verification;
- webhooks processed at most once;
- a pool deadlock in agent dispatch;
- media calls under a row lock without timeouts;
- shutdown cutting off recording starts, which are now reconciled;
- an exchange/revocation deadlock.

The Tier-1 agent changed in three ways (ADR-021):
- Its model IDs are checked against a model registry. Both former defaults have announced shutdowns, so production now refuses any model that has not been verified.
- Its provider endpoint is pinned.
- It can no longer chain tool calls.

Also added:
- `make audit`, a dependency scan, which upgraded two Go modules;
- `make provider-smoke`, an opt-in live-provider check, BLOCKED here for lack of a key;
- a data inventory, which lists six privacy gaps.

The architecture is turn-based with barge-in, **not full duplex** (ADR-021).

**Milestone B (2026-11-04).** The Tier-2 evaluation MVP (ADR-022) runs end to end on the local stack:
- **Handoff and queue.** The outbox relay applies T14 and publishes identifier-only events to the Redis stream `ati:tier2:events`. `services/tier2-evaluator` consumes the stream with acknowledgement, idle reclaim (`XAUTOCLAIM`), a heartbeat during slow evaluations, and a dead-letter queue. A dead-lettered interview still goes to human review.
- **Evidence access.** The worker reads evidence as `ati_tier2_evaluator`: read-only, without candidate identity.
- **Recording attempts.** It reports every attempt through the internal API. The control plane stores attempts raw.
- **Validation.** In trusted Go code (`internal/evaluation`), against the stored transcript: identity, integer scores and the recomputed weighted score, verbatim quotes, spans, references, and supporting evidence for every non-zero score. Only a valid attempt becomes the canonical evaluation.
- **Review and report.** Every answer goes to human review (T15). T16, an automatic report, is never fired. A reviewer decides each answer (T17), and that writes a staff-only report: decision support, with no hiring field.
- **Rubrics.** Versioned rubrics have their own contract; three synthetic demo rubrics cover the demo plans.
- **Evaluators.** Three adapters exist:
  - a deterministic reference evaluator, for synthetic rubrics only and refused in production;
  - OpenAI, through the Responses API with strict structured output and `store: false`;
  - Anthropic, through the streamed Messages API with structured output and no server-side fallbacks.

  The model adapters are verified only against recorded-shape responses: no provider credentials exist here. Evaluator quality against human labels is unmeasured (Milestone C).

Integrating the pipeline found and fixed four defects, each with a regression test:
- an interview whose question has no rubric could never leave review; it is now reviewed with a rationale only;
- a lowercase log level stopped the worker from starting;
- the closing remark's transcript was refused once the relay had already moved the interview on (T14 and T15
  within about a second of completion). The agent's closing duties now work in every post-interview state;
- the Tier-1 agent's logs could carry the candidate's words in livekit-agents warnings. The framework marks such
  extras `lk.pii.*`, and those are now redacted. The E2E log scan caught this one.

**Milestone C (2026-11-04).** [DATA_AND_ML_LIFECYCLE.md](../architecture/DATA_AND_ML_LIFECYCLE.md) sets out how evaluator quality is to be measured and governed:
- lineage;
- dataset rules: session-level splits, TR/EN strata, minimisation;
- the labelling protocol: two blind raters, adjudication above 20 points, kappa and Krippendorff's alpha;
- metrics and a fairness review;
- change governance: what a registry status change, a prompt change or retrieval requires.

The offline harness (`make tier2-bench`) runs in three steps:
1. It checks each item of a versioned dataset against the manifest's sha256 and refuses a session spanning splits.
2. It evaluates the items with the reference evaluator.
3. It validates every attempt with the control plane's own validator (`ati-admin validate-evaluations`) and reports
   validity, abstention and agreement.

The only dataset, `synthetic-v1`, has synthetic labels, so the run proves the mechanics and that the validator
accepts every reference attempt. It measures no model quality. No human labels exist.

## 1. Baseline audit (repository state before implementation)

The repository contained architecture documents only: `README.md`, `LICENSE` (Apache-2.0) and six files in `docs/architecture/` (about 1,500 lines). There were no source files, build files, tests, CI or infrastructure definitions.

- Functional implementation: 0%. MVP acceptance criteria met: 0/12. Production gates met: 0/5. Go-live checklist: 0/15.

## 2. Current status by milestone

| Milestone | Scope | Status |
|---|---|---|
| Phase 0 — contracts | State machine v1.1, schemas, prompts, content, fixtures, ADR-011–020, threat-model addendum, OpenAPI, test plan, hygiene checks | **Verified (local)**: `make checks`, `make contracts-test` (120 tests). Human review pending |
| M2 — Go engine, config, logging | Pure flow engine from the embedded contract; ADR-020 config refusals; redacting logger; HTTP plumbing; architecture tests | **Verified (local)**: `make go-lint` (vet + staticcheck), `make go-test` (model-based walk over 3,000 seeds: 208 completions / 2,792 cancellations with every invariant checked; fuzzing; refusal matrix; log sentinels) |
| M3 — Schema, roles, transition service | Phase-1 schema with DB-enforced INV-1/INV-9, append-only triggers, least-privilege roles; transactional transition service (row lock + `state_version`, per-step events and audit, outbox with contract validation); ingestion with speech-time attribution; completion protocol; leases with fencing | **Verified (local)**: `make go-test` against PostgreSQL 16 as the real application role (privilege matrix, lifecycle, button/deadline paths, pause/resume, withdrawal, expiry, consent, attribution, concurrency storms, compromised-agent suite) |
| M4 — Auth, API, LiveKit, ingestion, completion | Two listeners; staff/candidate/agent authentication; idempotency keys (credentials never stored for replays); invitation rate limit; staff invitation revocation (pauses a live interview and deletes its room); LiveKit rooms, mic-only tokens, server-side dispatch with nonce-only metadata, claim verification, signed and deduplicated webhooks, removal of participants the control plane did not mint (including a second agent while one holds the lease); Tier-1 context via restricted views; metrics; sweepers; binaries | **Verified (local)** against PostgreSQL with a fake media layer: HTTP flow to completion, cross-candidate/tenant isolation, CSRF, listener separation, idempotency replay/reuse and concurrent creation, staff token expiry and revocation, invitation revocation, claim attacks and expired nonces, fencing, webhook trust (decoded dedicated-key signatures), decoded candidate token grants, rate limiting, security headers and environment marker on every response, telemetry to `/metrics`, outbox negatives, log sentinels, OpenAPI route parity, ADR-020 refusals isolated per gate, startup smoke tests of the binary, `ati-admin` commands. Real LiveKit wiring: see M7 |
| M5 — Recording | Fail-closed policy (deployment flag and encryption attestation, tenant opt-in, candidate consent, explicit retention, voice mode, open question, microphone track), `start_recording` on T05/T13, stop on pause/withdraw/complete and track unpublish, LiveKit TrackEgress client with bounded calls (an unanswered start fails closed) and an asynchronous start, egress webhooks, `audio_ref` on candidate segments, Tier-1 `recording_active` view, purge with audited tombstones (`ati-admin recordings purge-expired`), tenant policy command | **Verified (local)** with a fake egress and an in-process S3 server (gofakes3): policy matrix (each precondition alone, including no open question), lifecycle including the stop on completion (T11), audio refs, failures, unanswered starts, idempotent purge, webhook routing; mutation-checked. **Verified (scripted, loopback)** against the real LiveKit server without an egress service: the recording fails closed and the interview continues. **A real recording is unverified**: no egress service in this environment |
| M6 — Tier-1 agent | LiveKit worker on explicit dispatch; claim, lease and fenced writes over the internal API (no database access); interview controller; single parameterless tool with grace, pending-transcript wait and flushed manifest; answer windows tied to the prompt's own playout; prompt-delivery marking; per-question turn profiles; measured `low_energy`; BLUEPRINT §5 latency metrics from per-reply measurements; pause on provider errors, errored session close and undelivered prompts; scripted engine (fake VAD/STT/LLM/TTS inside `AgentSession`, pitch-coded intents and failure injection) and OpenAI Realtime engine; local-only turn handling (no hosted LiveKit Inference); redacting JSON logs | **Verified (local)**: `make agent-test` (ruff, mypy strict, 176 pytest tests incl. mutation-checked properties). OpenAI engine: configuration and turn-profile updates unit-tested only, **not verified against the provider** (no credentials) |
| M7 — Media E2E | `tools/dev/stack.sh` (PostgreSQL, checksum-verified LiveKit OSS 1.13.11, control plane, agent); synthetic candidate over real WebRTC (`tests/e2e`); missing-agent sweeper, resume re-dispatch, room deletion after the closing remark | **Verified (scripted, loopback)**: `make e2e`, 21 synthetic-candidate tests (full plan, barge-in, disconnect/resume, pause intake, injection, provider failure, button path, agent crash; four injected provider failures; quiet speech flagged `low_energy`; metrics and policy flags on the control plane; the Turkish plan with per-question turn profiles; recording failing closed without an egress service; server-enforced media grants, intruder removal, deleted rooms, forged webhooks) plus post-run log and audio-file scans. Barge-in stop latency at the candidate, n=5: p50 392 ms, max 418 ms (loopback, uncalibrated 200 ms minimum interruption) |
| M8 — Web UI | Next.js 16 / React 19 candidate UI (`apps/web`): fragment-only invitation exchanged on click, purpose-level DRAFT consent, device check (a blocked microphone is explained as blocked), live room (mic-only publishing, unpublished while paused, captions from interviewer transcription, ARIA live status), pause/resume/finish/withdraw from `allowed_actions`, refresh rejoin, device-loss pause; per-request CSP nonce, strict security headers, lint guardrails, bundle secret scan | **Verified (local)**: `make web-test` (eslint, tsc, 229 unit tests, production build, bundle scan). **Verified (scripted, loopback)** in Chromium against the real stack, 7 flows (`tests/e2e/test_browser.py`): voice to completion; keyboard, device loss and withdrawal; text mode; consent refusal with the document security headers; refused microphone; no microphone device; leaving and returning to the room. axe-core audits every screen against WCAG 2.1 A/AA: no violations |
| A — Phase 1 audit closure (2026-11-04) | Webhook body bound and processing-aware dedupe; dispatch without a second pool connection; bounded media calls and idle-transaction limit; drained shutdown and recording reconciliation; sweepers that continue past a failing session; bounded metric labels; indexes (migration 00006); audited purge of operational records; session-first lock order; idempotency takeover of stale claims; IPv6 /64 rate limiting with a bounded table; loopback listeners by default; model registry, required model IDs and pinned endpoint (ADR-021); no chained tool calls; provider smoke harness; dependency scans; data inventory | **Verified (local)**: the regression test of each finding ([TEST_PLAN.md](../qa/TEST_PLAN.md#phase-1-audit-regressions-phase1_auditmd)), `make test`, `make e2e` and `make audit` (section 9). The live provider run is **Blocked** (no credentials) |
| B — Tier-2 evaluation MVP (2026-11-04) | ADR-022; state machine v1.2 (T17 in Phase 2); rubric contract and synthetic demo rubrics; migration 00007 (rubrics, raw attempts, canonical evaluations, review tasks and outcomes, reports; evaluator LOGIN with narrow views; `reviewer` role); outbox relay to Redis Streams; deterministic validator; internal Tier-2 API; reviewer queue, task, outcome and report endpoints; `services/tier2-evaluator` (consumer, evidence loader, reference/OpenAI/Anthropic adapters, configuration refusals); stack integration | **Verified (local)**: G (validator, relay with a real redis-server, service, API, store grants, architecture), T2 (`make tier2-test`: config, prompt pin and drift, reference evaluator, adapters against recorded shapes, consumer against a real redis-server, worker flow, redaction), E (`test_tier2.py`; synthetic voice answers abstained in `test_voice_interview.py`). Model evaluators **Implemented — unverified** (no credentials) |
| C — Data and ML foundations (2026-11-04) | Data and ML lifecycle document; offline benchmark harness (`tier2_evaluator.bench`: dataset manifest and leakage checks, quadratic-weighted kappa, Krippendorff's alpha, MAE, within-10, percentiles); `ati-admin validate-evaluations` (the Go validator offline); synthetic TR/EN dataset `synthetic-v1` | **Verified (local)**: metrics against hand-computed values and Krippendorff's published example, dataset integrity and leakage refusal (T2), the offline validator (G), `make tier2-bench` (every reference attempt accepted). **No benchmark of evaluator quality**: the labels are synthetic |
| M9 — Text mode | `POST /text-responses` (text mode only, activation-bound, refused during a pending completion; control-plane-written `candidate_text` segments; audit without text); Tier-1 view of the open question's text answers; agent passes each once as user input; text-mode greeting; UI offers text mode without a microphone | **Verified (local)**: G API tests (binding, mode, length, ordering, contract-valid context, manifest-free confirmation, no text in logs), A tests (user input only, once, other activations ignored). **Verified (scripted, loopback)** in Chromium with the microphone refused (`text_flow.mjs`): captions, no published track, completion |

## 3. Gap analysis (requirement → status)

| ID | Requirement (source) | Status | Evidence / target |
|---|---|---|---|
| G1 | Tier-1/Tier-2 isolation (ADR-001, BP §3.1) | **Verified (local)**: separate deployables and lockfiles; the agent has no database driver or credentials (dependency test, import boundaries); Tier-1 reads only allowlisted views through `ati_tier1_ctx`; the Tier-2 worker reads evidence as `ati_tier2_evaluator` (SELECT only, no candidate identity) and writes only through the internal API; ID-only outbox payloads and stream entries; compromised-agent suite | — |
| G2 | Modular control plane, contract tests (ADR-002) | **Verified (local)**: package boundaries (`internal/archtest`), embedded-contract drift test, OpenAPI route parity | — |
| G3 | LiveKit/WebRTC media (ADR-003) | **Verified (scripted, loopback)** (`make e2e`) | TURN, loss and jitter Blocked (no netem) |
| G4 | App-owned provider adapter (ADR-004) | `VoiceEngine` with scripted and OpenAI Realtime engines; scripted **Verified (local + E2E)** including per-question turn profiles; OpenAI **Implemented — unverified** (turn-profile updates unit-tested on the model; model IDs checked against the registry and the endpoint pinned, ADR-021; production refuses unverified models) | Live provider blocked: no credentials; `make provider-smoke` is ready to record evidence |
| G5 | PostgreSQL canonical; Qdrant projection (ADR-005) | PostgreSQL **Verified (local)** | Qdrant deliberately not introduced (ADR-022): it waits for a benchmark showing retrieval helps |
| G6 | Durable jobs, DLQ (ADR-006) | **Verified (local)**: transactional outbox; relay in `seq` order with T14 before publishing; Redis Streams consumer group with acknowledgement, reclaim, heartbeat, max deliveries and DLQ (real redis-server); dead-letter fallback to human review (E) | Redis restart and long outages: Phase 6 chaos |
| G7 | Evidence before score (ADR-007) | **Verified (local)**: the control plane validates every attempt against the stored transcript (identity, arithmetic, verbatim quotes, spans, references, support) and stores invalid output raw only | Validator rules uncalibrated against a model's real failure modes (no live model) |
| G8 | Human accountability (ADR-008) | **Verified (local)**: every answer is reviewed (T15, `policy_all_reviewed`); only a reviewer's decision (T17) produces a report; T16 is unwired; no decision fields | Candidate report and appeal: Phase 5 |
| G9 | Privacy by design (ADR-009) | **Verified (local)**: purpose-level DRAFT notices; recording optional, fail-closed, retention-bound with audited purge; audited purge of operational records; PII-minimal schema; redacted logs; E2E log and audio-file scans. Data inventory and flow map written ([DATA_INVENTORY_AND_FLOWS.md](../architecture/DATA_INVENTORY_AND_FLOWS.md)) | Gaps G1–G7 there: no transcript, timeline or evaluation-record purge, no deletion or export workflow, control-plane database TLS not enforced, provider terms unverified, streams trimmed by length only; legal review open; real egress unverified |
| G10 | No cheating inference (ADR-010) | Contract **Verified (local)** (no timing in handoff events) | — |
| G11 | State machine + invariants (BP §4) | **Verified (local)**: contract, pure engine (model-based walk, fuzzing), persisted transitions with per-step events and audit, database-enforced invariants | — |
| G12 | Latency metrics (BP §5, §16) | **Verified (scripted, loopback)**: TTFB, TTFA, end of turn (candidate turn end to first reply audio) and full response from the framework's per-reply measurements, plus barge-in stop latency, reach `/metrics` (E2E); candidate-side barge-in measured in E2E | Loopback and scripted only; no provider or network figures |
| G13 | Barge-in, turn policy (BP §6) | **Verified (scripted, loopback)**: barge-in stops playback without closing the answer; received-silence grace; frame starvation never ends a turn; each question's turn profile (`extended_pause` waits longer) | Thresholds uncalibrated (Phase 6) |
| G14 | Rubric and weights (BP §7) | **Verified (local)**: rubric contract v1 with the documented weights enforced by the contract tests and the validator; three synthetic demo rubrics (draft) referenced by both demo plans | Real rubrics need subject-matter authors and review |
| G15 | Tier-1 prompt and runtime context (BP §8, P&E §1) | **Verified (local)**: pinned prompt checked at startup; allowlisted context validated before use; candidate text never enters instructions | Live model behaviour unverified |
| G16 | Tier-2 prompt (P&E §2) | **Verified (local)**: the worker checks the sha256 pin at startup, appends versioned output rules, and sends the answer as one delimited data block (no synthetic indicators) | Live model behaviour unverified |
| G17 | Adversarial controls (BP §9) | Backend **Verified (local)**; scripted injection and forbidden tool call over real media change nothing; server-enforced media grants, intruder removal, forged webhooks and deleted rooms over real LiveKit (**Verified (scripted, loopback)**) | Live-model injection suite blocked (no credentials) |
| G18 | Coding sandbox, whiteboard (BP §10) | Not started | Phase 4 |
| G19 | HITL routing, override audit (BP §11) | **Verified (local)**: review reasons per answer (low confidence, transcript quality, missing segments, integrity flags, evaluator request, follow-up suggested, output risk, unsupported claims, contradictions, disagreement, invalid or abstained or failed or missing evaluation, pipeline failure, no rubric, synthetic source); override audit (reviewer, prior and revised scores, rationale, rubric version) | Thresholds (0.85, 0.6, 20 points) uncalibrated; reviewer UI not built (API only) |
| G20 | Data entities, transcript immutability (BP §13) | **Verified (local)** | Append-only grants + triggers tested even for the owner; answers bind exact segment IDs |
| G21 | API surface (BP §17) | **Verified (local)** for every Phase-1 endpoint, including text responses, and the Phase-2 internal Tier-2 operations, review queue, task, outcome and report | Appeals: Phase 5 |
| G22 | Observability (BP §16) | Prometheus metrics (HTTP with bounded method labels, transitions, voice latency, webhooks including `too_large` and `in_flight`, sweepers including recording reconciliation, policy flags); correlation IDs; redacted JSON logs in every process, checked by the E2E log scan | Dashboards and OTel export Phase 6 |
| G23 | QA/chaos matrix (BP §18) | Plan **Verified (local)** ([TEST_PLAN.md](../qa/TEST_PLAN.md)) | Per test plan |
| G24 | Text/non-audio fallback (TM §4–5) | **Verified (local; browser: scripted, loopback)**: text response mode end to end | A fully text interviewer (no audio output) and a broader accommodations process are Phase 5–6 |
| G25 | Threat model for new boundaries | Document written ([addendum](../architecture/THREAT_MODEL_ADDENDUM_PHASE1.md)) | Human review pending |
| G26 | Cost model usage capture (STACK §5) | Per-session usage recorded (**Verified (scripted, loopback)**: engine and audio durations) | Provider token counts need the live engine |
| G27 | Repository conventions | **Verified (local)** | `tools/checks/*` (including: no GitHub Actions workflows; `audit.sh` dependency scans), ADR immutability pin test |
| G28 | MVP acceptance criteria and production gates | Criteria 1–8 and 10–12 verified (1–3 with the scripted engine, 5–8 with the reference evaluator, loopback only); 9 belongs to Phase 5; production gates 0/5 | See section 4 |

## 4. MVP acceptance criteria (DECISIONS_AND_ROADMAP)

| # | Criterion | Status |
|---|---|---|
| 1 | Consented voice interview in a supported browser | **Verified (scripted, loopback)** in Chromium against the real stack (M8) and with a synthetic WebRTC participant (M7); live provider unverified |
| 2 | State survives reconnect without duplicates or corruption | **Verified (scripted, loopback)**: disconnect, agent crash and provider failure each pause and resume as a new activation; one answer per item (G concurrency suites + E) |
| 3 | Interruption stops playback and preserves state | **Verified (scripted, loopback)** (barge-in E2E) |
| 4 | Transcript persisted with speaker, timestamps, sequence, version | **Verified (local)** (G ingestion tests; E2E data checks) |
| 5 | Evaluation asynchronous, never blocks voice | **Verified (local + E2E)**: evaluation starts only from `interview.completed` through the outbox and Redis; nothing in the live path waits for it |
| 6 | Every claim links to transcript evidence and rubric criteria | **Verified (local)**: evidence items cite candidate segment IDs, a verbatim quote, a span and rubric reference IDs; non-zero scores need supporting evidence. Shown with the reference evaluator only |
| 7 | Backend validates schema, ranges, evidence, arithmetic | **Verified (local)** (`internal/evaluation` tests; E2E: the reference evaluator's output passes the Go validator) |
| 8 | Low-confidence or failed-integrity evaluation → human review | **Verified (local + E2E)**: every answer is reviewed in this phase, with the reasons recorded |
| 9 | Candidate report and appeal path | Phase 5 |
| 10 | No secrets, raw audio or sensitive data in logs | **Verified (local)**: G and A sentinel tests; E2E scans every process log for credentials, JWTs and transcript text and the file system for audio files |
| 11 | Tests for injection, duplicates, retry, provider failure, authorization | **Verified (local)** for the backend and agent; scripted injection and five kinds of provider failure over real media; live-model injection blocked |
| 12 | No automatic adverse decision from AI or telemetry | **Verified**: no decision fields or endpoints (C); no automatic report (T16 unwired, G); reports exist only after human review (G, E) |

## 5. Dependency and infrastructure assessment

| Item | Finding | Decision |
|---|---|---|
| Go | Local 1.24.7; pgx 5.12, LiveKit server-sdk-go 2.19 and protocol 1.53 require Go ≥ 1.26 | `go 1.26` with toolchain auto-download (verified reachable) |
| LiveKit Go APIs | `CreateDispatch`, `CreateRoom{DepartureTimeout, MaxParticipants}`, `CanPublishSources`, `CanPublishData`, `webhook.ReceiveWebhookEvent` present | Use |
| Python | 3.13 with uv; livekit-agents 1.8.10 (`AgentServer`, `AgentSession`, `RoomOptions`), OpenAI realtime engine (`semantic_vad`; model IDs from the registry, e.g. `gpt-realtime-2.1` and `gpt-transcribe`: the former defaults `gpt-realtime` and `gpt-4o-transcribe` shut down on 2027-01-20 and 2027-02-26) | Separate agent lockfile |
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
| 2 Evaluation | Outbox relay to Redis Streams (ack/retry/DLQ/reclaim); evaluator deployable with its own DB role and provider key; deterministic validator covering every P&E §8 test; transcript-version binding; review routing; Qdrant with canonical fallback; human-labelled benchmark; resolve C3/C4. **Status (Milestone B):** everything but the benchmark, a live evaluator run and Qdrant is verified locally; Qdrant is deferred by ADR-022; C3/C4 have proposed resolutions |
| 3 Interview intelligence | Isolated CV/JD parsing; versioned role/seniority plans; follow-up policy decided (C5) |
| 4 Coding and design | Sandbox passes the TM §5 sandbox matrix; Monaco; collaborative canvas |
| 5 Reports and review | Reviewer RBAC, override audit, candidate report and appeal, recording access with audit, access/export/deletion |
| 6 Hardening | All five production gates and the go-live checklist; latency by region, device and network; chaos; cost model |

## 7. Risks and unresolved conflicts

| ID | Item | Status |
|---|---|---|
| C1 | Pause only from QUESTION_ACTIVE; no withdrawal or expiry | Resolved by ADR-012 (owner-approved direction) |
| C2 | `evidence_honesty` naming; no quote or segment references | Resolved by ADR-013 (owner-approved direction) |
| C3 | `hallucination_risk` semantics undefined | Proposed in ADR-022: the risk of the evaluator's own output, stored as `output_risk` and shown to reviewers as `evaluator_output_risk`; never a property of the candidate, never scored |
| C4 | `evaluation_version` compared to rubric version; no model, prompt or transcript versions | Proposed in ADR-022: `evaluation_version` is the rubric reference; provider, model, prompt hash, evaluator, answer and transcript versions live in each attempt and evaluation record |
| C5 | Tier-2 `recommendation: FOLLOW_UP` vs "Tier-2 cannot steer the interview" | Proposed in ADR-022: advisory only (`advisory_next_step`), recorded as a review reason; live follow-ups come only from the plan; decide before Phase 3 |
| C6 | API surface missing consent, invitation, agent and webhook endpoints | Resolved by ADR-016 |
| C7 | Evidence transcript produced inside the Tier-1 provider session | Accepted MVP risk (ADR-014) |
| C8 | Consent gate vs lawful basis | Open (legal): purpose-level records; every basis is `to_be_determined_by_legal` |
| C9 | Append-only evidence vs erasure | Open: audited purge for recordings (ADR-017); general deletion Phase 5 |
| C10 | Diagram `Context → Qdrant` arrow could leak rubric data into Tier-1 | Resolved by design: the Tier-1 context has no rubric or Qdrant access |
| R1–R9 | Technical risks | See [threat-model addendum](../architecture/THREAT_MODEL_ADDENDUM_PHASE1.md) §3 and the ADR consequence sections |

## 8. Blockers

| Blocker | Affects | Unblock |
|---|---|---|
| No OpenAI or LiveKit Cloud credentials | Live voice path, live-model injection suite, provider latency, usage numbers, model verification (ADR-021) | Provide `OPENAI_API_KEY` (and optionally `LIVEKIT_URL`, `LIVEKIT_API_KEY`, `LIVEKIT_API_SECRET`) as environment secrets, then run `make provider-smoke` |
| No OpenAI or Anthropic credentials for Tier-2 | Live evaluator runs, evaluator latency and cost, model verification for `tier2_evaluator` (`claude-opus-5-5`, `gpt-6.1-sol` are `candidate`) | Provide `ANTHROPIC_API_KEY` and/or `OPENAI_API_KEY` as environment secrets; then run the worker with `ATI_TIER2_EVALUATOR=anthropic` or `openai` against synthetic interviews |
| No human-labelled evaluation data | Evaluator agreement with reviewers, calibration of confidence and routing thresholds | Labelling protocol and reviewers (Milestone C) |
| No Docker daemon | Container images, compose | A Docker-enabled environment |
| No egress service | Real recording | Egress deployment (Docker) |
| No `tc` / netem | Packet loss, jitter, TURN chaos | A lab machine with NET_ADMIN |
| Human review required | Phase 0 gate, legal notice approval, benchmarks, production gates | Architecture, legal and qualified reviewers |

## 9. How to verify

The repository does not use GitHub Actions (repository convention, enforced by `make checks`); these local commands are the verification of record.

| Command | What it runs | Result at this revision |
|---|---|---|
| `make checks` | repository hygiene (secrets, media files, PII patterns), Python import boundaries, generated state diagram | pass |
| `make contracts-test` | schema meta-validation and fixtures, state-machine structure, doc drift, isolation, OpenAPI, model registry, rubrics | 144 passed |
| `make go-lint` / `make go-test` | gofmt, vet, staticcheck; Go tests with the race detector against PostgreSQL 16 | pass (17 packages with tests, 0 skipped, including the Phase 1 audit regressions and the Tier-2 validator, relay, service, API and grant tests) |
| `make agent-test` | ruff, mypy (strict), pytest for the Tier-1 agent | 197 passed |
| `make tier2-test` | ruff, mypy (strict), pytest for the Tier-2 worker (consumer tests against a throwaway redis-server) and the benchmark harness | 114 passed |
| `make web-test` | eslint, tsc, vitest, production build, bundle secret scan | 229 passed; build and scan pass |
| `make e2e` | local stack (with Redis and the Tier-2 worker); synthetic WebRTC candidate (voice interview, failure injection and signals, recording, media security) and typed interviews through Tier-2; Chromium flows (each screen audited with axe-core); log and audio-file scans | 30 passed from a fresh stack in 13 min 44 s (scripted engine, reference evaluator, loopback), 2026-11-04 |
| `make audit` | govulncheck v1.9.0 (control plane), pip-audit 2.9.0 (every uv lockfile, including the Tier-2 worker's), pnpm audit (web, production) | ok: no vulnerable symbol reached (one advisory in a required module is not called by the code); no known vulnerabilities in the Python or web lockfiles |
| `make provider-smoke` | opt-in live OpenAI check (ADR-021) | BLOCKED: no `OPENAI_API_KEY` in this environment |
| `make tier2-bench` | offline benchmark on `synthetic-v1` with the reference evaluator and the Go validator (DATA_AND_ML_LIFECYCLE.md) | pass: 14/14 reference attempts accepted by the Go validator, 1 abstention (no candidate speech); agreement figures are on synthetic labels and say nothing about quality |

## 10. Known gaps and follow-ups

Phase 2 (Milestone B):
- The OpenAI and Anthropic evaluators are verified only against recorded-shape responses. Model behaviour, structured-output compatibility of the full provider schema, latency and cost are unknown until a live run with credentials. Both registry entries are `candidate`.
- No benchmark: agreement with human labels, confidence calibration and the routing thresholds (0.85 confidence, 0.6 transcript quality, 20-point disagreement) are uncalibrated policy values. Every answer is reviewed for this reason.
- No reviewer web UI: review and reports are API-only.
- The reference evaluator matches indicator phrases; it says nothing about correctness and exists only to exercise the pipeline.
- Raw attempts, evaluations, review outcomes and reports have no purge job yet (data inventory gap G1). The Redis streams are trimmed by length only (G7).
- One worker handles one entry at a time; there is no metrics exporter or health endpoint for the worker.
- No transcript-revision workflow exists, so "a revision creates a new evaluation" is verified only as a binding: attempts for another transcript version are refused, and the canonical key includes the version.

Phase 1:

- The OpenAI Realtime engine is configuration-tested only (including its turn-profile updates, registry checks and endpoint pin); a recorded live run (`make provider-smoke`) is part of the Phase 1 exit criteria. Which transcription replacement works inside a conversation session, and the latency of the reasoning realtime model, are open until then (ADR-021).
- Privacy gaps G1–G7 of the [data inventory](../architecture/DATA_INVENTORY_AND_FLOWS.md): no purge of transcripts, timelines, consent records, evaluation records or candidates; no deletion, export or appeal workflow; the control plane's database TLS not enforced; provider data terms unverified; streams trimmed by length only.
- `golang.org/x/crypto` advisory GO-2026-5932 (not reached by the code) awaits a tagged release with the fix.
- Interruption, grace, dispatch-timeout, endpointing (0.3 s / 1.2 s), `low_energy` (-35 dBFS), egress-timeout (10 s) and semantic-VAD eagerness values are uncalibrated placeholders (barge-in stop latency at the candidate: p50 about 0.4 s on loopback, dominated by the 200 ms minimum interruption duration).
- Blocked autoplay (the enable-audio control) is unit-tested only: automated Chromium grants a user activation to every scripted evaluation, so it cannot be reproduced end to end.
- Data packets from the candidate are dropped by LiveKit; the end-to-end test asserts the server-side permission, because a drop cannot be observed without a second legitimate subscriber.
- An agent-kind token minted with a leaked API key while no lease is held cannot be told apart from the dispatched replacement until it claims (claiming needs the single-use nonce); this is the documented trusted-infrastructure residual risk.
- No screen-reader run; keyboard, ARIA wiring and automated WCAG 2.1 A/AA checks (axe-core) are browser-tested.
- Container images and deployment manifests are not built (no Docker daemon).
