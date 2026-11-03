# Architecture Decision Records and Implementation Roadmap

**Version:** 1.0  
**Status:** Proposed decisions for MVP

## ADR-001 — Two-tier interaction and evaluation

**Status:** Accepted  
**Decision:** Separate Tier-1 realtime interviewer from Tier-2 asynchronous evaluator.

**Rationale:** Minimize voice latency, protect evaluation integrity, prevent scores from influencing conversational behavior, and isolate failure domains.

**Consequences:** Transcript/event pipeline and versioned evaluation contracts are required. The two tiers need separate permissions and observability.

## ADR-002 — Modular control plane before microservices

**Status:** Accepted for MVP  
**Decision:** Build the control plane as a modular backend with clear bounded contexts; deploy evaluator workers separately.

**Rationale:** Keep initial operations manageable while protecting async AI workloads from request-path contention.

**Consequences:** Enforce package/module boundaries and contract tests. Extract services only when scale, ownership, or reliability requires it.

## ADR-003 — WebRTC media transport

**Status:** Accepted for MVP  
**Decision:** Use LiveKit/WebRTC for browser audio; use WebSocket or durable events for control/transcript messaging where appropriate.

**Rationale:** WebRTC is purpose-built for real-time media. WebSocket may be simpler for text/control but requires custom audio framing and media handling.

**Consequences:** Test TURN behavior, reconnect, jitter, packet loss, device permissions, and cross-browser support.

## ADR-004 — Realtime provider adapter

**Status:** Accepted for MVP  
**Decision:** Start with OpenAI Realtime behind an application-owned provider interface.

**Rationale:** Accelerate MVP while retaining the option to benchmark chained STT + LLM + TTS alternatives.

**Consequences:** Normalize session lifecycle, tool boundaries, transcript events, cancellation, and telemetry behind adapters. Do not assume model-specific capabilities are portable.

## ADR-005 — PostgreSQL is canonical

**Status:** Accepted  
**Decision:** PostgreSQL owns session, transcript metadata, rubric versions, evaluation records, and audit state. Qdrant is a retrieval projection.

**Rationale:** Deterministic transactional records and traceability must not depend on vector search.

**Consequences:** Implement projection versioning, reconciliation, and a fallback to canonical rubric data if Qdrant is unavailable.

## ADR-006 — Durable evaluation jobs

**Status:** Accepted for MVP  
**Decision:** Use Redis Streams with consumer groups, acknowledgements, retries, idempotency, and a dead-letter path. Reassess Kafka/RabbitMQ when throughput and operational needs justify them.

**Rationale:** Plain Redis Pub/Sub does not provide durable consumer processing.

**Consequences:** Define retention, reclaiming pending entries, poison-message policy, and replay behavior.

## ADR-007 — Evidence before score

**Status:** Accepted  
**Decision:** Evaluator output must include verifiable evidence spans and rubric references. Backend recomputes weighted scores and validates the response.

**Rationale:** Reduce unsupported model judgments and score inflation.

**Consequences:** Keep immutable transcript versions, schema validation, evaluator versioning, and human-labelled benchmark sets.

## ADR-008 — Human accountability

**Status:** Accepted  
**Decision:** Do not automate final hiring decisions or adverse rejection based solely on AI scores or anomaly signals. Provide human review and appeal.

**Rationale:** Evaluation is fallible and employment decisions have significant consequences.

**Consequences:** Build reviewer RBAC, audit trail, explanations, and appeal lifecycle before production.

## ADR-009 — Privacy by design

**Status:** Accepted  
**Decision:** Minimize collected audio and telemetry, disclose processing, configure retention, encrypt stored/transmitted data, and support access/deletion workflows subject to legal requirements.

**Rationale:** Voice, transcript, resume, and assessment data are personal data.

**Consequences:** Legal approval and provider/subprocessor review are production gates.

## ADR-010 — No cheating inference from weak proxies

**Status:** Accepted  
**Decision:** Focus changes, response delays, paste events, and voice timing are contextual telemetry only. They cannot directly lower technical scores or prove cheating.

**Rationale:** Signals have many benign explanations, including accessibility needs and network conditions.

**Consequences:** Disclose collection, minimize it, offer accommodations, and require human review before adverse interpretation.

---

# Implementation roadmap

| Phase | Scope | Deliverables | Exit gate |
|---|---|---|---|
| 0 | Repository and contracts | ADRs, directory map, state machine, schema, threat model | Contracts reviewed |
| 1 | Voice MVP | UI, LiveKit, realtime adapter, state lifecycle, transcript | Real sessions and interruption/reconnect tests |
| 2 | Evaluation core | Event ingestion, durable queue, evaluator, retrieval, validator | Evidence-linked outputs; human benchmark |
| 3 | Interview intelligence | JD/CV parser, role/seniority rubric, follow-up policy | Versioned interview plan |
| 4 | Coding and design | Monaco, sandbox, collaborative whiteboard | Isolation and modality tests |
| 5 | Reporting and review | Candidate report, reviewer queue, appeals, audit | RBAC and audit verification |
| 6 | Production readiness | Security, privacy, chaos, load, cost, operations | All production gates passed |

## Workstream ownership

| Workstream | Accountable role | Key review partners |
|---|---|---|
| System boundaries and ADRs | Chief Architect | All leads |
| Realtime model and prompt chain | AI Architect | QA, backend |
| VAD and media reliability | Audio ML Engineer | Frontend, QA |
| Session API and persistence | Backend Engineer | Architect, security |
| Candidate/reviewer UI | Frontend Engineer | Backend, accessibility |
| Provider and competitor benchmark | Research Specialist | AI, architect |
| Infrastructure and data security | DevOps/Security Engineer | Backend, legal |
| Test strategy and chaos | QA/Chaos Engineer | All workstreams |

## MVP acceptance criteria

1. Candidate can complete a consented voice interview in a supported browser.
2. Interview state survives reconnect without duplicate answers or state corruption.
3. Candidate interruption reliably stops AI playback and preserves question state.
4. Transcript is persisted with speaker, timestamps, sequence, and version.
5. Evaluation executes asynchronously and does not block voice response.
6. Every material evaluation claim links to transcript evidence and rubric criteria.
7. Backend validates schema, score range, evidence references, and weighted arithmetic.
8. Low-confidence or failed-integrity evaluation routes to human review.
9. Candidate can access the approved report and appeal path.
10. Secrets, raw audio, and sensitive data are absent from ordinary application logs.
11. Automated tests cover injection, duplicates, retry, provider failure, and authorization.
12. No automatic adverse hiring decision is made from AI score or anomaly telemetry alone.

## Repository implementation conventions

- Keep architecture docs in `docs/architecture/`.
- Keep API contracts in `docs/contracts/` once endpoints are implemented.
- Keep ADRs versioned and immutable after acceptance; supersede with a new ADR.
- Keep prompts and JSON Schemas versioned alongside tests.
- Store no secrets, candidate PII, or real audio samples in Git.
- Add architecture diagrams as Mermaid source for reviewability.
