# Architecture documentation

This directory contains the initial architecture package for AI-Technical-Interviewer.

| Document | Purpose |
|---|---|
| [BLUEPRINT.md](./BLUEPRINT.md) | System design, tier separation, state machine, evaluation, privacy, APIs, roadmap |
| [PROMPT_AND_EVALUATION.md](./PROMPT_AND_EVALUATION.md) | Tier-1/Tier-2 prompt contracts, JSON Schema, validation, evidence policy |
| [DECISIONS_AND_ROADMAP.md](./DECISIONS_AND_ROADMAP.md) | Architecture Decision Records, workstreams, MVP acceptance criteria |
| [THREAT_MODEL_AND_QA.md](./THREAT_MODEL_AND_QA.md) | Threat model, privacy controls, chaos and QA plan |
| [STACK_AND_BENCHMARK.md](./STACK_AND_BENCHMARK.md) | Technology comparisons and reproducible benchmark protocol |
| [adr/](./adr/README.md) | ADR-011 onward (Proposed): Phase 0/1 decisions where the documents above are silent or in conflict |
| [THREAT_MODEL_ADDENDUM_PHASE1.md](./THREAT_MODEL_ADDENDUM_PHASE1.md) | Additional trust boundaries, threats and controls introduced by the Phase 1 design |
| [DIRECTORY_MAP.md](./DIRECTORY_MAP.md) | Repository layout |

These documents describe an architecture proposal, not an implemented or benchmarked product. Performance budgets, weights, and confidence thresholds are hypotheses until validated.

Start with [BLUEPRINT.md](./BLUEPRINT.md), then review ADRs and the prompt/evaluation contract before implementation.

Related: machine-readable contracts in [contracts/](../../contracts/README.md), the HTTP API in [docs/contracts/openapi](../contracts/openapi/control-plane.v1.yaml), the [test plan](../qa/TEST_PLAN.md) and the [implementation status](../status/IMPLEMENTATION_STATUS.md).
