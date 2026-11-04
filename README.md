# AI-Technical-Interviewer

A voice-native technical interview platform designed around a two-tier architecture: a natural real-time interviewer and an independent, evidence-based evaluation engine.

## Architecture documentation

The initial enterprise architecture package is available in [docs/architecture](./docs/architecture/README.md).

- [System Architecture Blueprint](./docs/architecture/BLUEPRINT.md)
- [Prompt Architecture & Evaluation Contract](./docs/architecture/PROMPT_AND_EVALUATION.md)
- [Architecture Decisions & Implementation Roadmap](./docs/architecture/DECISIONS_AND_ROADMAP.md)
- [Threat Model, Privacy & QA Plan](./docs/architecture/THREAT_MODEL_AND_QA.md)
- [Technology Selection & Benchmark Plan](./docs/architecture/STACK_AND_BENCHMARK.md)
- [Proposed ADRs (ADR-011 onward)](./docs/architecture/adr/README.md) and the [Phase 1 threat-model addendum](./docs/architecture/THREAT_MODEL_ADDENDUM_PHASE1.md)

## Core architecture principles

- Tier-1 realtime interviewer is isolated from Tier-2 scoring.
- Interview state and permissions are enforced by backend code, not prompts.
- Evaluation is asynchronous, versioned, evidence-based, and deterministically validated.
- Human review and candidate appeal are part of the product design.
- Audio, transcript, CV, and telemetry are handled with privacy-by-design controls.

## Repository layout

Machine-readable contracts (state machine, JSON Schemas, prompts) live in [contracts/](./contracts/README.md), the HTTP API contract in [docs/contracts/openapi](./docs/contracts/openapi/control-plane.v1.yaml), and services under `services/` and `apps/`. See the [directory map](./docs/architecture/DIRECTORY_MAP.md).

## Status

**Pre-production. Not for use with real candidates.** What is built, verified, blocked or not started is tracked in [docs/status/IMPLEMENTATION_STATUS.md](./docs/status/IMPLEMENTATION_STATUS.md), and the test-to-requirement mapping is in [docs/qa/TEST_PLAN.md](./docs/qa/TEST_PLAN.md).

- Phase 0 contracts are implemented and covered by automated tests. They are **Proposed** until human architecture review.
- The Phase 1 voice MVP (control plane, Tier-1 voice agent, candidate web UI, optional fail-closed recording, text response mode) runs end to end locally with a deterministic **scripted** voice engine over loopback WebRTC. The OpenAI Realtime engine, real recording egress and real networks are not verified here.
- The Tier-2 evaluation MVP (ADR-022) runs end to end locally: an outbox relay to Redis Streams, an evaluator worker with retries and a dead-letter fallback, deterministic validation of every evaluator claim against the stored transcript, mandatory human review of every answer, and a staff-only report. Only a deterministic **reference** evaluator on synthetic rubrics runs here; the OpenAI and Anthropic evaluators are not verified against the live providers, and evaluator quality is unmeasured.
- Reviewers work in a review console in the web app (ADR-023). They sign in with a short-lived session cookie, and see the question, the transcript and the rubric before they choose to open the AI draft, which never prefills their decision.
- Latency targets, rubric weights and confidence thresholds remain hypotheses that require benchmarks and human validation.
- No component makes or recommends hiring decisions: reports are decision support built from human review.

## Development

Requirements: Python 3.12+ with [uv](https://docs.astral.sh/uv/), Go 1.26 (the toolchain downloads itself), Node 22 with pnpm, PostgreSQL 16 binaries for the local test cluster, and `redis-server` (Redis 7) for the Tier-2 tests and the local stack.

```bash
make checks          # repository hygiene and import boundaries (standard library only)
make contracts-test  # contract, drift, isolation and OpenAPI tests
make go-lint go-test # control plane: gofmt, vet, staticcheck; tests against an ephemeral PostgreSQL
make agent-test      # Tier-1 agent: ruff, mypy (strict), pytest
make tier2-test      # Tier-2 evaluator: ruff, mypy (strict), pytest (starts a throwaway local redis-server)
make web-test        # web UI: eslint, tsc, unit tests, production build, bundle scan
make e2e             # local stack (PostgreSQL, Redis, LiveKit OSS, control plane, scripted agent, Tier-2 worker, web UI) and E2E
```

`make stack-up` starts the same local stack for manual use (`tools/dev/stack.sh`, development placeholders only). Each service has its own README.

This repository does not use GitHub Actions (see the [repository conventions](./docs/architecture/DECISIONS_AND_ROADMAP.md#repository-implementation-conventions)); `make test` and `make e2e` are the verification of record.
