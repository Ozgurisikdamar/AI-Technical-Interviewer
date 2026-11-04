# Contracts

Machine-readable contracts shared by every service, with their tests (ADR-019). The architecture documents remain authoritative. Drift tests fail when a contract here diverges from them.

| Path | Contract | Source |
|---|---|---|
| `state-machine/interview-session.v1.2.json` | Interview lifecycle: states, transitions with actors and guards, request R01, invariants (v1.2: T17 in Phase 2) | BLUEPRINT §4 + ADR-012 + ADR-022 |
| `state-machine/interview-session.v1.2.mmd` | Mermaid diagram **generated** from the JSON contract | `tools/generate_state_diagram.py` |
| `schemas/evaluation/v1/` | Tier-2 evaluation response (amended v1) | P&E §3 v1.1 + ADR-013 |
| `schemas/transcript/v1/` | Canonical transcript segment; Tier-1 segment submission | BLUEPRINT §13 + ADR-014 |
| `schemas/tier1/v1/` | Tier-1 runtime context allowlist | P&E §1 + ADR-011 |
| `schemas/plan/v1/` | Versioned interview plan (unconditional follow-ups) | P&E §5 + ADR-012 |
| `schemas/consent/v1/` | Purpose-level candidate notice | TM §4 + ADR-017/018 |
| `schemas/events/v1/` | Outbox handoff events (identifiers only) | ADR-015 |
| `schemas/models/v1/`, `models/model-registry.v1.json` | Provider model registry: which model IDs services may be configured with, their status and shutdown dates | ADR-021 |
| `schemas/rubric/v1/`, `content/rubrics/` | Versioned question rubrics (criteria per dimension, weights, approved references); the shipped ones are synthetic demo rubrics | P&E §3–5 + ADR-022 |
| `prompts/` | Tier-1 and Tier-2 templates, verbatim, sha256-pinned in `manifest.json` | P&E §1–2 |
| `content/` | Synthetic demo plans, rubrics and DRAFT notices (en, tr), not for real candidates | — |
| `fixtures/` | Valid and invalid instances for every schema | P&E §8 |

All contracts are **Proposed** until reviewed. Run the tests with:

```bash
make contracts-test
```

Regenerate the diagram after editing the state machine:

```bash
python3 contracts/tools/generate_state_diagram.py
```
