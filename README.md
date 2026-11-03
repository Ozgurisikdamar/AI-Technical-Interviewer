# AI-Technical-Interviewer

A voice-native technical interview platform designed around a two-tier architecture: a natural real-time interviewer and an independent, evidence-based evaluation engine.

## Architecture documentation

The initial enterprise architecture package is available in [docs/architecture](./docs/architecture/README.md).

- [System Architecture Blueprint](./docs/architecture/BLUEPRINT.md)
- [Prompt Architecture & Evaluation Contract](./docs/architecture/PROMPT_AND_EVALUATION.md)
- [Architecture Decisions & Implementation Roadmap](./docs/architecture/DECISIONS_AND_ROADMAP.md)
- [Threat Model, Privacy & QA Plan](./docs/architecture/THREAT_MODEL_AND_QA.md)
- [Technology Selection & Benchmark Plan](./docs/architecture/STACK_AND_BENCHMARK.md)

## Core architecture principles

- Tier-1 realtime interviewer is isolated from Tier-2 scoring.
- Interview state and permissions are enforced by backend code, not prompts.
- Evaluation is asynchronous, versioned, evidence-based, and deterministically validated.
- Human review and candidate appeal are part of the product design.
- Audio, transcript, CV, and telemetry are handled with privacy-by-design controls.

## Status

**Architecture and planning documents only.** This repository has not yet been verified as an implemented or production-ready application. Latency targets, rubric weights, and confidence thresholds are initial hypotheses that require benchmarks and human validation.

See [the architecture index](./docs/architecture/README.md) to get started.
