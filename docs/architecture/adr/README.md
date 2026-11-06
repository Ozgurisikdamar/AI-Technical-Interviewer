# Architecture Decision Records (ADR-011 onward)

ADR-001 to ADR-010 are accepted and live in [DECISIONS_AND_ROADMAP.md](../DECISIONS_AND_ROADMAP.md). They are immutable: a contract test pins a hash of each section, and changes must be made by a superseding ADR.

The records below are **Proposed**. They capture decisions needed to implement Phase 0 and Phase 1 (and, from ADR-021, the Phase 1 audit and Phase 2) where the accepted documents are silent, ambiguous or in conflict. Code in this repository implements them, but they stay Proposed until an architecture review accepts them. Rejecting one means changing the code that depends on it.

| ADR | Title | Status |
|---|---|---|
| [ADR-011](./ADR-011-tier1-agent-runtime-and-tool-surface.md) | Tier-1 agent runtime, voice engines and tool surface | Proposed |
| [ADR-012](./ADR-012-interview-state-machine-v1-1.md) | Interview state machine v1.1 and answer completion protocol | Proposed (owner-approved direction) |
| [ADR-013](./ADR-013-evaluation-contract-v1-amendments.md) | Evaluation contract v1 amendments | Proposed (owner-approved direction) |
| [ADR-014](./ADR-014-session-timeline-transcript-ingestion.md) | Session timeline, transcript ingestion, attribution and provenance | Proposed |
| [ADR-015](./ADR-015-transactional-outbox-handoff.md) | Transactional outbox as the only Tier-1 to Tier-2 handoff | Proposed |
| [ADR-016](./ADR-016-authn-authz-and-api-surface.md) | Authentication, authorization, agent leases and API surface | Proposed |
| [ADR-017](./ADR-017-audio-recording.md) | Optional audio recording, off by default | Proposed (owner-approved direction) |
| [ADR-018](./ADR-018-text-response-mode.md) | Text response mode | Proposed (owner-approved direction) |
| [ADR-019](./ADR-019-repository-layout-and-contract-locations.md) | Repository layout and contract locations | Proposed |
| [ADR-020](./ADR-020-environment-gating-and-non-production-safeguards.md) | Environment gating and non-production safeguards | Proposed |
| [ADR-021](./ADR-021-model-registry-endpoint-pinning-and-duplex-assessment.md) | Provider model registry, endpoint pinning and the full-duplex assessment | Proposed |
| [ADR-022](./ADR-022-tier2-evaluation-pipeline-mvp.md) | Tier-2 evaluation pipeline MVP: outbox relay, Redis Streams, deterministic validation, mandatory human review (amends ADR-012: contract v1.2) | Proposed |
| [ADR-023](./ADR-023-review-console-staff-sessions.md) | Review console: staff browser sessions and independent-first review (amends ADR-016) | Proposed (owner-approved direction) |
| [ADR-024](./ADR-024-candidate-sign-in.md) | Candidate sign-in with an application number and an access code (amends ADR-016) | Proposed (owner-approved direction) |

"Owner-approved direction" means the project owner chose the option when it was raised as a conflict between documents; the full record still needs architecture review.
