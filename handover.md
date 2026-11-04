# HANDOVER — Latest Review & Implementation Instructions for Claude

> **LATEST UPDATE — 2026-11-04 (UTC): Milestone A (Phase 1 audit closure) implemented on `main`**
>
> Read this update first. The owner's review below (based on `545983f`) remains the instruction baseline, and its priorities still apply. This section records what has been done against it since then, what was verified, and what remains.

### Done in Milestone A

- **Audit report.** [docs/qa/PHASE1_AUDIT.md](docs/qa/PHASE1_AUDIT.md) checks the handover findings against the code.
  - 15 control-plane defects and 3 Tier-1 defects were confirmed and fixed. Each has a regression test, and 11 were reproduced on the old code.
  - The most serious were: an unbounded webhook body read before signature verification; webhooks processed at most once; a pool deadlock in agent dispatch; media calls under a row lock without timeouts; and shutdown cutting off recording starts, which are now reconciled.
- **Models and full duplex (ADR-021).**
  - Both former defaults are deprecated: `gpt-realtime` shuts down on 2027-01-20 and `gpt-4o-transcribe` on 2027-02-26.
  - Model IDs now come from `contracts/models/model-registry.v1.json` and have no defaults. Production refuses unverified models, and the provider endpoint is pinned.
  - The model cannot chain tool calls (`MAX_TOOL_STEPS=0`; with 1 it could).
  - The architecture is turn-based with barge-in, **not full duplex**. ADR-021 records why, and the benchmark a duplex prototype would need.
- **P0.1.** `make provider-smoke` is an opt-in live check. It covers model availability, tool behaviour under injection attempts, Turkish and English transcription, and latency percentiles, and writes redacted evidence to `docs/status/evidence/`. It reports BLOCKED here because there is no key.
- **P0.3.** [docs/architecture/DATA_INVENTORY_AND_FLOWS.md](docs/architecture/DATA_INVENTORY_AND_FLOWS.md) holds the data-flow map and table inventory. Its gaps G1–G6 include: no transcript, timeline or candidate purge; no deletion, export or appeal workflow; database TLS not enforced; provider data terms unverified.
- **P0.4.**
  - `make audit` runs govulncheck, pip-audit and pnpm audit over every lockfile. It found nothing reachable; `x/net` and `cel-go` were upgraded. One unreachable `x/crypto` advisory still awaits a release.
  - `ati-admin purge-operational` removes old webhook-dedupe and idempotency records, with an audit entry.

### Verification at this revision

Each command below ran on 2026-11-04 in this environment:

| Command | Result |
|---|---|
| `make test` | pass (repository checks; 129 contract tests; Go lint and 15 Go packages; 197 agent tests; web lint, typecheck, 229 unit tests, build and bundle scan) |
| `make e2e` | 28 passed from a fresh stack in 13 min 33 s (scripted engine, loopback) |
| `make audit` | ok (see above) |
| `make provider-smoke` | BLOCKED (no key) |

The live voice path, real networks and container images remain unverified.

### Still open or blocked

- **Blocked:**
  - live provider verification (needs `OPENAI_API_KEY` as an environment secret; then run `make provider-smoke`);
  - Docker, real egress, TURN and netem;
  - every human gate (architecture, legal, annotators, production gates).
- **Not started yet:** Milestone B and later (Tier-2 evaluation, review, report, data/ML). The owner's sections 4–6 below are the plan.

### Next: Milestone B, the Tier-2 MVP, with every evaluation reviewed by a human

1. An outbox relay to Redis Streams (ADR-006): consumer groups, reclaim, retries and a DLQ. The relay applies T14.
2. Versioned rubrics (synthetic demo rubrics for the shipped plans).
3. A Tier-2 worker with a deterministic reference evaluator and OpenAI and Anthropic adapters. It uses a read-only database role and the idempotency key `answer + transcript version + rubric version + evaluator version`.
4. A deterministic validator in the control plane. It checks the schema, verbatim quotes against candidate segments of the answer, millisecond bounds, rubric references and the weighted arithmetic.
5. Raw attempts stored separately from canonical evaluations.
6. Every evaluation routed to human review: T15, then T17. T17 moves to Phase 2 in state-machine contract v1.2; T16 stays unwired.
7. Staff-only review-queue, review-outcome and report endpoints, with no hiring-decision field.
8. E2E coverage, from a text-mode interview to a report.

The guard tests that block Phase 2 must be narrowed deliberately, not deleted:
- `test_openapi.py`;
- `internal/archtest`;
- `store_test.go` (the evaluator role's LOGIN);
- `flow` MaxPhase.

---

> **HANDOVER REVIEW — 2026-11-04 (UTC), owner's remote review of `545983f` (kept as written)**

> **LATEST HANDOVER — 2026-11-04 (UTC)**
>
> This section is the current authoritative handover. Read it first. Previous handover sections, if any, must remain below this section; do not delete historical decisions. This review is based on the GitHub `main` tree at commit `545983fb48a88625b4d8c98d391e94cd90fb5733` and repository documentation/code inspection. It is a remote source review; the reviewer did **not** independently execute the local test suite.

## 0. Instructions to Claude — read before coding

You are taking over an actively developed voice-native technical interview platform. Read this document first, then read the entire repository, especially:

- `CLAUDE.md`
- `README.md`
- `docs/status/IMPLEMENTATION_STATUS.md`
- `docs/qa/TEST_PLAN.md`
- `docs/architecture/BLUEPRINT.md`
- `docs/architecture/THREAT_MODEL_ADDENDUM_PHASE1.md`
- `docs/architecture/adr/ADR-011...` through `ADR-020...`
- `contracts/` (schemas, prompts, plans, state machine, fixtures)
- `services/control-plane/`
- `services/tier1-voice-agent/`
- `apps/web/`
- `tests/e2e/`

**Do not start by rewriting architecture or blindly adding features.** Perform a fresh code audit against this handover, status report, contracts and test plan. Identify discrepancies between documentation and implementation, fix them, then proceed by priority. Do not claim a requirement is done unless code + automated test + actual integration evidence supports the claim.

Repository rules:
- **No GitHub Actions** under `.github/workflows/` or `.github/actions/`. This is a deliberate repository convention enforced by `make checks`. Do not add CI workflows.
- Run verification locally using `make test` and `make e2e`; report exact commands and output.
- Never put real secrets, candidate PII, raw audio, credentials, or production logs in Git.
- Keep all changes in `main` unless the owner explicitly requests otherwise.
- Keep this `handover.md` current: prepend a dated, concise latest handover section at the top after each substantial review; preserve prior history beneath it.

---

# 1. Executive review

## Current implementation reality

This repository has progressed beyond a documentation-only project. It currently contains:

- Go control plane with PostgreSQL-backed interview state machine, API, authentication, consent handling, LiveKit room/dispatch integration, transcript ingestion, leases/fencing, audit and optional recording.
- Python Tier-1 LiveKit voice agent with scripted engine and OpenAI Realtime engine.
- Next.js/React/TypeScript candidate interview UI with consent, mic checks, room controls, captions, pause/resume/withdraw, text alternative and browser tests.
- Contracts and JSON Schemas, state machine, prompt templates, fixtures, ADRs, threat-model and QA traceability.
- Local scripted WebRTC E2E tests and Chromium browser tests.

**It is a Phase-1 voice-interview MVP foundation, not a complete technical interview product and not production-ready.** It has no implemented Tier-2 evaluator, candidate assessment report, reviewer/human-in-the-loop workflow, coding sandbox, whiteboard, Qdrant/RAG retrieval service, or production evaluation/data science pipeline.

The repo's own `docs/status/IMPLEMENTATION_STATUS.md` is appropriately candid: the OpenAI live-provider path is unverified; current full E2E is scripted/loopback; production gates are 0/5; real candidates are not cleared.

## What is strong (preserve, do not regress)

1. Tier-1/Tier-2 isolation is considered in contracts and backend boundaries; the voice agent is not allowed to score answers.
2. State machine and backend-authoritative transitions, state versioning, leases/fencing, idempotency, append-only evidence/audit, and constrained Tier-1 context are solid foundations.
3. Candidate media grants are deliberately narrow; microphone-only publication, no candidate data publishing, server-side room/agent dispatch, webhook signature checks and participant validation are tested.
4. Recording is optional and fail-closed with consent, tenant opt-in, retention, encryption attestation, and audited purge.
5. Text mode exists and supports candidate response when microphone access is denied.
6. Contract fixtures, schema validation, model-based state tests, adversarial agent tests, test harness, log scans and accessibility checks show strong engineering intent.
7. The repo clearly distinguishes scripted/loopback tests from real-provider verification.
8. Secrets are intended to stay out of client bundle and are checked by a bundle scan.

Do not remove these protections to make demos easier.

---

# 2. Current stack and models — actual code vs missing pieces

## Implemented / present

| Layer | Current implementation / repo evidence | Notes |
|---|---|---|
| Candidate frontend | Next.js 16, React 19, TypeScript 6, `livekit-client` | Candidate flow, not full employer/reviewer suite |
| Backend/control plane | Go (repository's toolchain is Go 1.26), PostgreSQL 16 | State, APIs, authorization, event/audit |
| Realtime media | LiveKit OSS + WebRTC | Local test stack, scripted E2E |
| Voice agent framework | LiveKit Agents Python | Worker/controller |
| Voice provider | OpenAI Realtime plugin | Live-provider verification missing |
| Current configured realtime model default | `gpt-realtime` in agent config | Alias, not pinned; verify current supported model IDs and change policy |
| Transcription | `gpt-4o-transcribe` configured | Live audio transcription quality has not been measured in the repo |
| VAD/turn detection | OpenAI semantic VAD, `medium/low` eagerness profiles | Values are uncalibrated; model currently is turn-based rather than a true full-duplex model |
| Test model | Scripted fake VAD/STT/LLM/TTS inside AgentSession | Synthetic signal/voice only; never production |
| DB | PostgreSQL canonical source | No Tier-2 evaluation records yet |
| Observability | Prometheus metrics + structured redacted logs | No full OpenTelemetry tracing/dashboard evidence |
| Object/audio storage | S3-compatible interface + local/fake S3 tests; LiveKit Egress integration | Actual production Egress/storage deployment not verified |

## Not implemented / absent

- Tier-2 evaluator model/service.
- Evaluation orchestration, independent judges, score aggregation and backend deterministic validation.
- Model selection/benchmark registry for evaluator (no chosen GPT/Claude/Gemini/etc. evaluator model documented as implemented).
- Embedding model, vectorization service, Qdrant/Milvus/Pinecone integration, vector index schema or RAG pipeline.
- Rubric retrieval and trusted technical source corpus; Qdrant remains a future architecture proposal, not current code.
- JD/resume ingestion/parsing, skill extraction, rubric generation and prompt-injection-safe document pipeline.
- Coding assessment model pipeline, AST/static analysis/test runner, isolated code execution sandbox.
- System-design whiteboard/canvas evaluation.
- Final report generator, candidate-facing results/report UI.
- Reviewer/admin interface, HITL queue, appeal, human override and audit UI.
- Dataset lifecycle, consented/curated evaluation datasets, annotation guidelines, label QA, benchmark suite, calibration and drift monitoring.
- Training/fine-tuning/LoRA pipeline. No trained custom model is present; this product currently uses provider models and a scripted test engine.
- Full-duplex architecture is not yet proven. Current `OpenAIRealtimeEngine` uses semantic VAD and a turn-based response model. If full-duplex concurrent listening/speaking is a hard product requirement, evaluate GPT-Live/full-duplex provider support or another architecture; don't describe current turn-based Realtime as full duplex.

**Important current API/model check:** OpenAI's current official docs list newer Realtime model identifiers (e.g. GPT Realtime 2.1 / 2.1 mini) and a distinct GPT-Live full-duplex model. Verify exact provider support, package compatibility, region, pricing and lifecycle against current official docs before changing the default. Do not switch production model by name alone—run compatibility and benchmark tests first.

---

# 3. P0 — Must fix / prove before calling Phase 1 complete

These are blockers for an externally usable product; they are not all necessarily code defects. Some are missing validation/evidence.

## P0.1 Live-provider integration and end-to-end verification

- Run a real OpenAI Realtime session with a dedicated, scoped secret supplied only via environment/secret manager (never commit it).
- Verify provider handshake, selected model ID, transcription language behavior, turn-profile update/session update behavior, barge-in, output audio, transcript events, provider errors, timeouts and reconnect.
- Capture redacted test evidence: model ID/version, provider request IDs, test date/region, latency p50/p95/p99, failures and usage. Never record raw candidate audio by default.
- Run actual injection attempts against the live model, including spoken/text prompt injection, hidden prompt disclosure, scoring manipulation and follow-up boundary cases.
- Make sure current docs do not claim live-provider behavior as verified until this passes.

## P0.2 Full-duplex claim and turn-taking

- Confirm whether current product acceptance is full duplex or turn-based.
- Current OpenAI engine uses Realtime semantic VAD, `create_response=True`, `interrupt_response=True`; this is not by itself evidence of simultaneous listen-and-speak full-duplex behavior.
- If full-duplex is required, prototype GPT-Live or a compatible full-duplex engine using LiveKit/provider integration. Preserve backend delegation and control-plane state authority.
- Benchmark both candidates on identical interview scripts, Turkish/English, pause, interruption, noisy microphone, and network tests.
- Avoid claiming sub-800 ms until real end-to-end provider measurements exist. Loopback synthetic latency is not production latency.

## P0.3 Production gating, privacy notice and candidate data governance

- Current candidate notices are explicitly **DRAFT**; do not use with real candidates.
- Resolve legal basis by processing purpose with legal counsel; consent should not be treated as universally sufficient or as a forced blanket acceptance.
- Establish jurisdictional analysis (KVKK, GDPR where relevant, employment/AI assessment rules, provider transfers/subprocessors and data residency).
- Define and implement retention, access/export/correction/deletion, candidate appeal, recording deletion, backup deletion and audit retention.
- Verify actual storage encryption, key management, TLS/SRTP, region/residency, Egress bucket policies, lifecycle and access control.
- Add a documented data-flow map and data inventory: audio, transcripts, CV/JD, candidate identifiers, reviewer notes, score/report, telemetry, provider payloads.
- Confirm recording off-by-default and fail-closed policy in a real deployment; prove egress start/stop/purge on a real Egress service.
- Make sure raw audio/transcript never leak through debug logs, error traces, metrics labels or provider SDK logs.

## P0.4 Operational security / release safety

- Verify real TLS/WSS termination and trusted proxy configuration; validate proxy headers/IP handling with the deployment topology.
- Review token/cookie properties, expiration, rotation, revoke behavior, invitation token lifecycle, CORS/origin boundaries, CSRF protections, rate limits and brute-force protection.
- Run dependency vulnerability scans (Go, Python, pnpm), license review, SBOM generation, secret scan and container scan. No GitHub Actions; use documented local scripts or an explicitly approved CI service via new ADR.
- Add a release process: pinned build versions, reproducible builds, signed/tagged release, rollback, migration rollback/forward strategy and incident response.
- Verify database backup/restore and disaster recovery; current local tests do not prove production backup resilience.
- Validate deployment config refuses real candidate sessions until every gate is reviewed and signed by a named person.
- Conduct an independent security review / penetration test before production.

---

# 4. P1 — Backend and distributed systems

## 4.1 Control plane/API

Audit every public/internal endpoint against OpenAPI and ensure:
- authentication, authorization, tenant scoping and object-level authorization are explicit;
- public and internal listeners remain separated;
- idempotency keys, replay semantics, state version, leases/fencing and nonce expiration are consistent;
- malformed/oversized JSON, request body limits, timeouts, rate limits and error handling are safe;
- API responses do not expose hidden state, internal tokens, rubric, transcript beyond permitted scope, or reviewer information;
- all state transitions and consent updates are atomic and auditable;
- webhook signature timestamp/age, replay prevention, key rotation and payload size are validated;
- unexpected participants and room/token revocation are tested under race conditions;
- health/readiness/liveness checks distinguish dependencies and do not expose sensitive detail;
- shutdown drains jobs/leases/egress and does not corrupt active interviews.

## 4.2 PostgreSQL

- Review every migration for constraints, lock behavior, transaction isolation, indexes, query plans, tenant boundaries and rollback safety.
- Confirm append-only evidence and audit behavior with least-privilege DB roles; ensure app users cannot bypass via table owner/superuser privileges in production.
- Define partitioning/retention strategy for high-volume events/transcript/metrics if load tests justify it.
- Test concurrent creation, answer submission, lease expiry, revocation, webhook replay, finalization, migrations and restore.
- Define outbox delivery semantics (at-least-once expected), idempotent consumers and poison-message/DLQ behavior. Transactional outbox exists; relay/consumer/DLQ is still a later phase.
- Add DB connection pool saturation, slow query and failure tests.

## 4.3 Queue / async jobs

Redis Streams / durable job relay and Tier-2 workers are **not yet implemented**. The current transactional outbox is not a complete async pipeline.

Implement:
- outbox publisher/relay, durable broker/stream, consumer groups, ack/reclaim, retries with exponential backoff, DLQ, replay and dedupe;
- idempotency per `session_id + answer_id + transcript_version + rubric_version + evaluator_version`;
- timeout/cancellation, poison payload, worker crash, provider rate-limit and partial output handling;
- queue lag, retries, DLQ, worker saturation and cost metrics;
- no evaluator backpressure on the live audio path.

## 4.4 APIs and product endpoints still missing

- evaluation job/result
- report retrieval
- reviewer queue and decision
- candidate appeal
- rubric/JD/resume management
- coding exercise/session
- whiteboard session/events
- data deletion/export workflow
- administrative tenant configuration with strong RBAC

Do not add a public score/hiring-decision endpoint without governance approval.

---

# 5. P1 — Data & ML / evaluation infrastructure (major product gap)

This is the biggest gap after the voice MVP. A voice interviewer alone is not a technical interview evaluation product.

## 5.1 Build the data lifecycle first

Create a versioned data contract and lineage:
- interview/session/question/answer identifiers;
- exact prompt, rubric, provider/model, model parameters and transcript version;
- source transcript segments and evidence span IDs;
- evaluation output, reviewer corrections and appeal result;
- language, role, seniority, skill taxonomy and assessment type;
- dataset split and consent/retention metadata;
- anonymization/pseudonymization and deletion lineage.

Raw audio is sensitive. Keep it separate from derived text and only retain when there is a documented purpose, permission, retention period and security controls.

## 5.2 Dataset and annotation requirements

Before any evaluator model can be considered reliable:
- build a licensed/consented benchmark set with human-reviewed technical answers;
- cover multiple skills, seniority levels, languages (at minimum Turkish and English), short/long answers, code/system design, uncertainty, partial correctness, misconceptions and edge cases;
- create annotation rubric and evidence span guidelines;
- use at least two qualified reviewers for a meaningful subset, adjudicate disagreements, track inter-rater reliability;
- split by candidate/source/question to prevent leakage (not random transcript chunks);
- hold out a test set; prevent rubric/reference overlap from contaminating evaluation;
- document data provenance, bias limitations, access and retention;
- never train on candidate data without a separately approved purpose and legal basis.

Do not start fine-tuning until the data quality and baseline evaluation justify it.

## 5.3 Evaluator pipeline (Phase 2)

Recommended pipeline:
1. Answer finalized event from outbox.
2. Retrieve immutable transcript segment IDs and question activation.
3. Resolve pinned rubric/question/version.
4. Run transcript-quality checks; abstain on incomplete/low quality.
5. Retrieve trusted technical references (if needed).
6. Extract atomic claims and rubric criterion evidence.
7. Evaluate dimension-specific criteria with one or more independent evaluator calls.
8. Validate JSON Schema in application code.
9. Validate every quote/span against actual transcript segment text, speaker, timestamp and transcript version.
10. Validate reference IDs/source trust, model/prompt/rubric version and score bounds.
11. Compute weighted score in deterministic backend code (not LLM arithmetic).
12. Evaluate evaluator disagreement and confidence routing.
13. Route to human review where required.
14. Persist raw model response separately from validated canonical result.
15. Generate report only from canonical validated result.

**Critical:** LLM confidence is not a calibrated probability. 0.85 is a routing policy threshold until calibrated on labelled validation data. Do not make a confidence number look scientifically valid without calibration.

## 5.4 Model strategy

Do not prematurely declare one model the winner. Create provider/model adapters and compare:
- OpenAI reasoning/text model for evaluator (current models must be chosen based on API support and benchmark);
- Anthropic Claude model as alternative;
- Gemini model as alternative if it provides useful multimodal/structured reasoning.

For the evaluator, prefer **transcript/text input + rubric + retrieved references** first, not raw audio inference. This reduces dependence on accent/prosody and makes evaluation auditable. Realtime voice model is for interaction, not scoring.

Benchmark on:
- technical accuracy and criterion-level agreement;
- false-positive incorrect claims and false-negative missed errors;
- evidence citation/quote validity;
- score inflation and seniority calibration;
- evaluator disagreement;
- Turkish/English parity;
- injection resistance;
- cost/answer and latency;
- abstention quality and reviewer overturn rate.

Do not use model-generated synthetic data as the sole benchmark. Synthetic data may expand test cases but must be separately labelled.

## 5.5 Vector DB / RAG

Qdrant is a blueprint choice only; it is not currently implemented. Implement only if retrieval materially improves evaluator performance:
- PostgreSQL remains canonical for rubric/version/source metadata.
- Use Qdrant as a rebuildable projection, not source of truth.
- Use stable chunk IDs, source IDs, language, skill, rubric version, provenance, trust level and access scope in payload.
- Separate tenant/private data from public reference material.
- Implement hybrid dense + lexical retrieval, reranking if justified, source allowlists and prompt-injection treatment.
- Benchmark retrieval recall@k, precision@k, nDCG, citation support, stale index and deletion propagation.
- Add Qdrant outage/fallback, index rebuild, projection lag, poison document and cross-tenant isolation tests.
- Keep retrieved text as untrusted data, not model instructions.
- Avoid vectorizing personal data without necessity and explicit governance.

Embedding model selection must be a benchmarked ADR: multilingual quality (TR/EN), dimensions, cost, latency, licensing, data handling, update/versioning and retrieval quality. No embedding model has been selected/implemented in the current repo.

## 5.6 Fairness and validity

- Technical answer quality must be separated from speaking style, accent, pitch, fluency, personality and confidence.
- Do not use emotion recognition or voice-based honesty/cheating inference.
- Analyze error rates and score distributions by relevant, legally reviewed cohorts; avoid collecting sensitive attributes without an approved lawful basis.
- Measure reviewer overturn rates, calibration, subgroup disparity and accommodations.
- Use role/seniority-specific rubrics that are published/pinned before interview.
- Keep human decision-maker accountable; do not automate hiring rejection.
- Provide candidate explanation, correction and appeal mechanisms.

---

# 6. P1 — Frontend UI/UX and accessibility

The existing candidate flow is a functional MVP, not a polished full product.

## Candidate UI

- Conduct visual UX review at mobile/tablet/desktop sizes; test real Safari, Chrome, Firefox, iOS and Android as supported.
- Improve interview state feedback: connecting, reconnecting, AI speaking/listening, candidate speaking, paused, transcript pending, question delivered, provider outage, session recovery.
- Add accessible audio visualizer and clear mic/camera permission rationale (camera not required unless whiteboard/video is introduced).
- Add explicit privacy/recording indicator, retention information and easy stop/withdraw control.
- Improve captions: partial/final transcript, language, latency, transcript correction/report error.
- Add network-quality warning and resilient reconnect, including recovery if tab sleeps or browser throttles.
- Ensure mobile audio autoplay/gesture handling and device switch.
- Provide text-only interviewer mode (not just candidate text answer while AI audio remains unavailable), screen-reader path and keyboard-only complete flow.
- Test screen readers (NVDA/VoiceOver), not just axe-core. Automated axe checks are not proof of accessibility.
- Test WCAG 2.2 AA target (repo currently reports WCAG 2.1 A/AA automated checks); include focus order, visible focus, contrast, target sizes, reduced motion, localization and zoom.
- Ensure Turkish localization quality and English copy consistency; test mixed language.
- Add candidate error recovery and meaningful support/report issue path.
- Do not add anti-cheating surveillance UI without legal/privacy approval.

## Employer / interviewer / reviewer UI (missing)

Design and implement:
- tenant/company onboarding and roles;
- job description and role/seniority/rubric configuration;
- candidate invitation management/revocation;
- interview schedule/session status;
- rubric/question preview and version pinning;
- review queue and evidence-linked answer assessment;
- candidate report view and appeal/override UI;
- audit trail, consent status, retention and data deletion controls;
- admin health/metrics without PII by default.

Use role-based access, audit and tenant isolation for every route. Reviewer UI must not expose candidate data to unrelated tenant users.

---

# 7. P1 — QA, tests and reliability

The current local suite is substantial, but its results are mostly scripted/loopback and cannot validate production voice quality.

## Existing test evidence in repository docs (not independently rerun by this reviewer)

At the latest documented revision:
- `make checks`: pass.
- `make contracts-test`: 120 passed.
- Go tests/race detector: pass (15 packages with tests).
- Tier-1 agent tests: 176 passed (ruff, mypy strict, pytest).
- Web: 229 tests plus lint/typecheck/build/bundle scan pass.
- E2E: 28/28 from fresh stack: 21 synthetic candidate WebRTC tests + 7 Chromium browser flows, scripted engine, loopback.
- Browser screens audited with axe-core; no reported violations.
- Candidate-side barge-in stop latency in loopback: p50 ~392 ms, max ~418 ms, n=5; not calibrated production performance.

These are valuable tests, but the 28/28 result is **not** proof of live model, real-world network or production reliability.

## Missing / weak coverage

- Real provider E2E and provider-side injection.
- TURN relay and real packet-loss/jitter/bandwidth shaping.
- Multiple browser engines/device platforms and actual mobile devices.
- Real Egress and object storage integration.
- Screen-reader testing and accessibility user testing.
- Load/stress tests for sessions, agent capacity, API, DB, queue, provider rate limits and cost.
- Database outage/recovery, backup/restore and disaster recovery.
- Tier-2 evaluator integration, malformed/partial evaluator output, retry/replay and DLQ.
- Dataset and model benchmark evaluation.
- End-to-end report/reviewer/appeal workflow.
- Tenant isolation across every new service and object store.
- Long-running sessions, browser tab sleep, memory/resource leaks.
- Dependency CVE/SBOM/container scanning.
- Production deploy, rollback and migration tests.

## Test strategy required

- Unit + contract + integration + E2E + security + performance + accessibility.
- Use deterministic fake model for state/logic tests, but provider-specific test suites must be separately labelled.
- Build reproducible test profiles: local scripted, provider sandbox, staging/production-like, chaos/network.
- Store test artifacts without candidate PII/audio.
- Every bug fix gets regression test.
- Track coverage by requirements and risk, not only line coverage.
- Do not claim “test coverage 100%” without report and scope.

---

# 8. P2 — Observability, SRE and production operations

- Add OpenTelemetry traces across browser/API/control plane/agent/provider/outbox/evaluator; correlate by opaque IDs only.
- Build dashboards for end-to-end TTFA, end-of-turn latency, barge-in latency, transcript quality, provider errors, room/agent state, queue lag, evaluation duration, cost, worker saturation, review/appeal rate.
- Alert on session stuck state, expired lease, missing agent, outbox lag, DLQ growth, repeated provider errors, recording retention failure and unexpected egress.
- Define SLOs by real benchmark: latency p50/p95/p99, successful completion, reconnect success, provider availability, queue completion.
- Add per-tenant rate limits, quotas and cost controls; protect against unbounded audio/session duration and token spend.
- Add graceful shutdown and worker drain; verify LiveKit rooms and recording cleanup.
- Provide runbooks for provider outage, DB failure, compromised token, data deletion, recording failure and rollback.
- Create capacity plan: agent concurrent sessions, CPU/memory, WebRTC bandwidth, TURN egress, DB pool, provider limits, object storage.
- Deployment architecture (Kubernetes, autoscaling, regions, TURN) should follow benchmark and load evidence; avoid speculative complexity before the MVP works.
- No Docker daemon was available in the previous test environment; build and scan images in a Docker-enabled environment.

---

# 9. Prioritized implementation sequence

Claude should work in small verifiable milestones; do not attempt an enormous one-shot rewrite.

## Milestone A — Audit and Phase-1 closure (first)
- Reconcile docs and actual code at current HEAD.
- Verify all current local tests.
- Real OpenAI Realtime provider integration in a credentialed sandbox.
- Confirm model ID compatibility and turn profile updates.
- Test live model barge-in, transcript accuracy, prompt injection and provider failure.
- Add provider/network benchmark report.
- Fix any defects found and update `docs/status/IMPLEMENTATION_STATUS.md`.

**Exit:** exact model/provider tested; no claims of full duplex or latency target without measured proof.

## Milestone B — Tier-2 evaluation MVP
- Async event relay, durable queue/consumer/DLQ/idempotency.
- Rubric storage/versioning and evaluator adapter.
- Transcript evidence verification and deterministic score arithmetic.
- Human review fallback (initially minimal secure queue; no auto decision).
- Test benchmark dataset and evaluator quality report.
- Persist evaluation version/model/prompt/transcript/rubric lineage.

**Exit:** test answer → evaluation → validated score/evidence → human review/report path; all asynchronous and failure-safe.

## Milestone C — Data & ML foundation
- Dataset/data lineage and annotation governance.
- Evaluator benchmark/score calibration/fairness analysis.
- Retrieval/RAG prototype with trusted source corpus.
- Decide embeddings/Qdrant only after retrieval benchmark and ADR.
- Add multilingual evaluation and Turkish-English parity.

**Exit:** quality metrics and benchmark show clear benefit; model choice documented and reproducible.

## Milestone D — Employer/reviewer product
- Employer/admin/reviewer RBAC UI.
- JD/rubric/question setup, invitation management.
- Evidence-based report, reviewer workflow, override, candidate appeal.
- Retention/export/deletion controls.

## Milestone E — Coding and system design interview
- Monaco/editor, test harness, AST/static analysis, secure sandbox.
- Whiteboard and system design artifacts.
- Sandbox isolation/chaos/security review.
- Separate modality-specific rubrics and evidence.

## Milestone F — Production readiness
- Real TURN/network tests, load/capacity, Egress, deployment, backup/restore.
- Privacy/legal gates, threat model, pen test, data deletion verification.
- OTel/SLO dashboards, alerts, runbooks, rollback.
- Human governance and accessibility signoff.

---

# 10. Required deliverables from Claude for this handover

Before and after implementation, commit these artifacts:

1. **Audit report**: code-level finding, path + line, severity, reproducibility, impact, remediation.
2. **Updated implementation status**: exact verified vs scripted vs blocked vs not-started.
3. **Model registry/ADR**: model/provider IDs, role (voice/STT/evaluator/embedding), API/version, data flow, region, cost, latency, benchmark, fallback. Clearly mark unknowns.
4. **Data/ML design**: dataset, labeling, rubric, evaluator, retrieval, vector schema, model validation, privacy and fairness.
5. **QA matrix**: requirement → test → exact command → result → remaining gap.
6. **Security review**: authz/tenant boundaries, prompt injection, token handling, audio/PII retention, dependencies, sandbox and deployment.
7. **UI/UX report**: candidate + recruiter/reviewer experiences, responsive, accessibility and recovery.
8. **Implementation plan**: small PR-sized changes with acceptance criteria and dependency order.
9. **Tests and regression fixes**: no undocumented behavior changes.
10. **Release note**: what is actually implemented, what remains blocked, and how to run locally.

## Final instruction

Prioritize correctness, candidate privacy, fairness, security, evidence and auditability over feature count or impressive demo behavior. Do not use voice biometrics, emotion/personality analysis, accent scoring, or inferred cheating as a technical score. Do not auto-reject candidates. The product must support accountable human review and candidate appeal.

---

# 11. References checked for model/security updates

- OpenAI Realtime guide: https://developers.openai.com/api/docs/guides/realtime
- OpenAI voice agents / full-duplex vs Realtime vs chained pipeline: https://developers.openai.com/api/docs/guides/voice-agents
- OpenAI GPT-Live guide: https://developers.openai.com/api/docs/guides/live
- OpenAI model catalog: https://developers.openai.com/api/docs/models
- LiveKit OpenAI Realtime plugin: https://docs.livekit.io/agents/models/realtime/openai/
- LiveKit realtime model overview: https://docs.livekit.io/agents/models/realtime/
- OWASP GenAI LLM Top 10 2026: https://genai.owasp.org/resource/owasp-genai-llm-top-10-2026/
- OWASP LLM Top 10 2025: https://owasp.org/www-project-top-10-for-large-language-model-applications/

