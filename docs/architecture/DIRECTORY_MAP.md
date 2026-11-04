# Directory map

Recorded by [ADR-019](./adr/ADR-019-repository-layout-and-contract-locations.md). Planned directories are marked; the [implementation status](../status/IMPLEMENTATION_STATUS.md) says what is actually built and verified.

```text
.
├── README.md
├── Makefile                         top-level entry points: checks, contracts-test, go-test, agent-test, tier2-test, web-test, e2e
├── contracts/                       machine-readable contracts + their tests (single source of truth)
│   ├── state-machine/               interview-session v1.2 JSON, its format schema, generated Mermaid
│   ├── schemas/<name>/v<N>/         JSON Schemas (Draft 2020-12): evaluation, transcript, tier1, plan, consent, events, models, rubric
│   ├── models/                      provider model registry (ADR-021): allowed model IDs, status, shutdown dates
│   ├── prompts/<name>/v<N>/         verbatim prompt templates + manifest.json (sha256 pins)
│   ├── content/                     synthetic demo plans and rubrics, DRAFT candidate notices (en, tr)
│   ├── fixtures/<schema>/{valid,invalid}/
│   ├── tests/                       pytest: schemas, state machine, doc drift, isolation, OpenAPI
│   └── tools/                       generators (state diagram)
├── docs/
│   ├── architecture/                authoritative architecture package (+ adr/ for ADR-011 onward)
│   ├── contracts/openapi/           HTTP API contract (control plane v1)
│   ├── qa/TEST_PLAN.md              QA matrix → test IDs and status
│   ├── qa/PHASE1_AUDIT.md           Phase 1 audit: findings, fixes, regression tests
│   ├── status/IMPLEMENTATION_STATUS.md   audit, gap analysis, verified status, blockers
│   └── status/evidence/             machine-written verification evidence (e.g. provider smoke runs; redacted)
├── services/
│   ├── control-plane/               Go: API, flow engine, persistence, LiveKit integration; outbox relay (internal/outbox),
│   │                                evaluation validator (internal/evaluation), review and report service (internal/interview)
│   ├── tier1-voice-agent/           Python: LiveKit Agents Tier-1 interviewer
│   └── tier2-evaluator/             Python: Tier-2 evaluation worker (Redis Streams consumer, read-only evidence loader,
│                                    reference / OpenAI / Anthropic evaluators; ADR-022); bench/ holds versioned
│                                    benchmark datasets for the offline harness (DATA_AND_ML_LIFECYCLE.md)
├── apps/web/                        Next.js candidate UI
├── tests/e2e/                       cross-service E2E (media over real WebRTC, browser flows, Tier-2 pipeline)
├── tools/
│   ├── checks/                      dependency-free repository checks (hygiene, import boundaries); audit.sh (dependency scans)
│   └── dev/                         local stack scripts (PostgreSQL, Redis, LiveKit OSS); contract sync scripts
```

Tier-2 evaluation workers (`services/tier2-*`) are separate deployables with their own lockfile, database role (`ati_tier2_evaluator`, read-only) and provider credentials. The import-boundary check enforces their isolation rules (no LiveKit, no Tier-1 modules), and `internal/archtest` keeps the control plane's validator a pure function.
