# Directory map

Recorded by [ADR-019](./adr/ADR-019-repository-layout-and-contract-locations.md). Planned directories are marked; the [implementation status](../status/IMPLEMENTATION_STATUS.md) says what is actually built and verified.

```text
.
├── README.md
├── Makefile                         top-level entry points: checks, contracts-test, go-test, agent-test, web-test, e2e
├── contracts/                       machine-readable contracts + their tests (single source of truth)
│   ├── state-machine/               interview-session v1.1 JSON, its format schema, generated Mermaid
│   ├── schemas/<name>/v<N>/         JSON Schemas (Draft 2020-12): evaluation, transcript, tier1, plan, consent, events, models
│   ├── models/                      provider model registry (ADR-021): allowed model IDs, status, shutdown dates
│   ├── prompts/<name>/v<N>/         verbatim prompt templates + manifest.json (sha256 pins)
│   ├── content/                     synthetic demo plans and DRAFT candidate notices (en, tr)
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
│   ├── control-plane/               Go: API, flow engine, persistence, LiveKit integration (planned → Phase 1)
│   └── tier1-voice-agent/           Python: LiveKit Agents Tier-1 interviewer (planned → Phase 1)
├── apps/web/                        Next.js candidate UI (planned → Phase 1)
├── tests/e2e/                       cross-service E2E (media over real WebRTC; planned → Phase 1)
├── tools/
│   ├── checks/                      dependency-free repository checks (hygiene, import boundaries); audit.sh (dependency scans)
│   └── dev/                         local stack scripts (PostgreSQL, LiveKit OSS) (planned → Phase 1)
```

Tier-2 evaluation workers (`services/tier2-*`) arrive in Phase 2 as a separate deployable with their own lockfile, database role and provider credentials. The import-boundary check already enforces their isolation rules.
