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
- Latency targets, rubric weights and confidence thresholds remain hypotheses that require benchmarks and human validation.
- No component makes or recommends hiring decisions.

## Development

Requirements: Python 3.12+ with [uv](https://docs.astral.sh/uv/), plus Go 1.26, Node 22 and PostgreSQL 16 as the services land.

```bash
make checks          # repository hygiene and import boundaries (standard library only)
make contracts-test  # contract, drift, isolation and OpenAPI tests
```
