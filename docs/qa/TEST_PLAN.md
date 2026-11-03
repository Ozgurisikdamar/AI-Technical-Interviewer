# Test plan and QA traceability

Maps every item of THREAT_MODEL_AND_QA §5, BLUEPRINT §18 and PROMPT_AND_EVALUATION §8 to a test, phase and status.

Status values:
- **Verified**: a test exists, runs in this repository and passes.
- **Verified (scripted, loopback)**: passes with the deterministic scripted engine over local WebRTC only.
- **Planned (Mx)**: scheduled in a Phase-1 milestone, not yet verified.
- **Blocked**: cannot run in the current environment (reason given).
- **Phase N**: belongs to a later phase.

Test locations: `contracts/tests` (C), `services/control-plane` Go tests (G), `services/tier1-voice-agent/tests` (A), `apps/web` unit and Playwright tests (W), `tests/e2e` media tests (E).

## Audio and network (TM §5)

| ID | Scenario | Phase | Test | Status |
|---|---|---|---|---|
| QA-AN-01 | Microphone permission denied | 1 | W: mic-denied path → text mode | Planned (M8) |
| QA-AN-02 | No microphone device | 1 | W: device check | Planned (M8) |
| QA-AN-03 | Device removed mid-session | 1 | W: device-loss patch → pause | Planned (M8) |
| QA-AN-04 | Browser tab suspended | 6 | — | Phase 6 |
| QA-AN-05 | 1/5/10% packet loss | 6 | — | Blocked: no `tc`/netem in this environment |
| QA-AN-06 | High jitter, bandwidth throttling | 6 | — | Blocked: no `tc`/netem |
| QA-AN-07 | TURN relay path | 6 | — | Blocked: no TURN/relay lab |
| QA-AN-08 | Reconnect after temporary disconnect | 1 | E + W: refresh/disconnect → PAUSED → resume | Planned (M7, M8) |
| QA-AN-09 | Candidate speaks while AI output is active (barge-in) | 1 | E: barge-in stop latency, state preserved | Planned (M7) |
| QA-AN-10 | Candidate silence vs packet loss | 1 | A: frame starvation ≠ silence; E: track loss pauses turn-taking | Planned (M6, M7) |
| QA-AN-11 | Multiple browsers and devices | 6 | — | Phase 6 (Chromium only in Phase 1) |

## State and data integrity (TM §5)

| ID | Scenario | Phase | Test | Status |
|---|---|---|---|---|
| QA-SD-01 | Duplicate answer event | 1 | G: concurrent/duplicate completion requests | Planned (M3, M4) |
| QA-SD-02 | Duplicate evaluation job | 2 | — | Phase 2 |
| QA-SD-03 | Out-of-order transcript segments | 1 | G: ingestion ordering | Planned (M4) |
| QA-SD-04 | Missing transcript segment | 1 | G: flush deadline → `missing_segments=true` | Planned (M4) |
| QA-SD-05 | Transcript revision after evaluation | 2 | — | Phase 2 (append-only versions in place from M3) |
| QA-SD-06 | Concurrent state transition requests | 1 | G: `-race` concurrency suite | Planned (M3) |
| QA-SD-07 | Session pause/resume race | 1 | G: concurrent pause/resume | Planned (M3) |
| QA-SD-08 | Worker restart during processing | 1 | E: agent crash → lease expiry → re-dispatch | Planned (M7) |
| QA-SD-09 | Database timeout and recovery | 6 | — | Phase 6 |
| QA-SD-10 | Queue consumer crash and reclaim | 2 | — | Phase 2 |
| QA-SD-11 | Qdrant unavailability, canonical fallback | 2 | — | Phase 2 |

## AI and prompt security (TM §5, P&E §7)

| ID | Scenario | Phase | Test | Status |
|---|---|---|---|---|
| QA-AI-01 | Spoken "ignore all previous instructions" | 1 | A + E: adversarial scripted transcripts (system-level blast radius); live model | Planned (M6); live model Blocked: no provider credentials |
| QA-AI-02 | Ask for system prompt or hidden rubric | 1 | A: rubric never in context; output monitor flags disclosure | Planned (M6) |
| QA-AI-03 | Attempt to change score | 1 | C: no score field or endpoint exists; G: no score route | Verified (C: `test_api_exposes_no_scoring_or_decision_surface`, `test_tier1_context_contains_no_evaluation_concepts`) |
| QA-AI-04 | Prompt injection in resume | 3 | — | Phase 3 |
| QA-AI-05 | Prompt injection in job description | 3 | — | Phase 3 |
| QA-AI-06 | Instruction embedded in retrieved reference | 2 | — | Phase 2 |
| QA-AI-07 | Tool invocation without authorization | 1 | G: compromised-agent suite; A: tool schema has no args | Planned (M4, M6) |
| QA-AI-08 | Malformed evaluator JSON | 2 | C: schema rejects malformed fixtures; validator Phase 2 | Partial: schema fixtures Verified (C) |
| QA-AI-09 | Fabricated evidence timestamps | 2 | — | Phase 2 (schema requires segment IDs + quote: Verified C) |
| QA-AI-10 | Unsupported technical claims | 2 | — | Phase 2 |
| QA-AI-11 | Contradictory answer segments | 2 | — | Phase 2 |
| QA-AI-12 | Evaluator disagreement, review routing | 2 | — | Phase 2 |

## Coding sandbox (TM §5)

| ID | Scenario | Phase | Status |
|---|---|---|---|
| QA-SB-01..10 | Infinite loop, memory exhaustion, fork, filesystem traversal, network, huge output, compiler crash, time limit, cleanup, tenant isolation | 4 | Phase 4 |

## Fairness and accessibility (TM §5)

| ID | Scenario | Phase | Test | Status |
|---|---|---|---|---|
| QA-FA-01 | Turkish and English sessions | 1 | C: en/tr plans + notices; A: language propagated to engine | Partial: content Verified (C); engine Planned (M6) |
| QA-FA-02 | Code-switching | 6 | — | Phase 6 (needs consented recordings) |
| QA-FA-03 | Varied accents and speech rates | 6 | — | Phase 6 |
| QA-FA-04 | Speech disability accommodations | 1 | W: text mode; ADR-018 | Planned (M9) |
| QA-FA-05 | Text-only alternative | 1 | G + A + W: text responses | Planned (M9) |
| QA-FA-06 | Long thinking pauses | 1 | A: `extended_pause` turn profile; no time-based auto-finalization | Planned (M6) |
| QA-FA-07 | Low-quality microphone | 6 | — | Phase 6 |
| QA-FA-08 | Screen reader and keyboard navigation | 1 | W: keyboard path, ARIA live region | Planned (M8) |
| QA-FA-09 | No scoring on accent or vocal traits | 1–2 | C: no such fields; recordings never used for voice assessment | Verified (C) structurally |
| QA-FA-10 | Human review of disputed assessment | 5 | — | Phase 5 |

## BLUEPRINT §18 required scenarios

| ID | Scenario | Maps to | Status |
|---|---|---|---|
| BP18-01 | 1/5/10% loss, high jitter | QA-AN-05/06 | Blocked |
| BP18-02 | Mic denied, device switch | QA-AN-01/03 | Planned (M8) |
| BP18-03 | Refresh and reconnect | QA-AN-08 | Planned (M7, M8) |
| BP18-04 | Barge-in during speech | QA-AN-09 | Planned (M7) |
| BP18-05 | Duplicate/out-of-order transcript events | QA-SD-01/03 | Planned (M4) |
| BP18-06 | Realtime, STT, evaluator timeout | A: failure injection → PAUSED | Planned (M6, M7); evaluator Phase 2 |
| BP18-07 | Rate limits and provider outage | A: failure injection; G: invitation rate limit | Planned (M4, M6) |
| BP18-08 | PostgreSQL / Redis / Qdrant restart | QA-SD-09/11 | Phase 2/6 |
| BP18-09 | Malformed evaluator JSON, partial output | QA-AI-08 | Phase 2 |
| BP18-10 | Worker crash and retry duplication | QA-SD-08 | Planned (M7) |
| BP18-11 | Prompt injection in audio/resume/JD/retrieval | QA-AI-01/04/05/06 | Audio Planned (M6); rest Phase 2/3 |
| BP18-12 | Unauthorized API/tool access | G: authZ, cross-tenant, compromised agent | Planned (M4) |
| BP18-13 | Concurrent session creation | G: concurrent create + idempotency | Planned (M4) |
| BP18-14 | Appeal after report | — | Phase 5 |
| BP18-15 | Accessibility and non-audio fallback | QA-FA-05/08 | Planned (M8, M9) |
| BP18-INV | Voice failure must not corrupt evaluation state | E: provider failure → PAUSED, no finalization | Planned (M7) |

## PROMPT_AND_EVALUATION §8 required automated tests

| ID | Test | Phase | Status |
|---|---|---|---|
| PE8-01 | Schema valid/invalid fixtures | 0 | Verified (C: `test_schemas.py`) |
| PE8-02 | Score boundaries and exact weighted arithmetic | 0/2 | Boundaries Verified (C fixtures); arithmetic Phase 2 validator; weights cross-doc Verified (C: `test_weights_agree_across_blueprint_validator_and_schema`) |
| PE8-03 | Invalid/unknown rubric version | 2 | Phase 2 |
| PE8-04 | Evidence timestamps beyond transcript length | 2 | Phase 2 |
| PE8-05 | Evidence span from interviewer instead of candidate | 2 | Phase 2 |
| PE8-06 | Empty or missing transcript segments | 1–2 | `missing_segments` flag Planned (M4); evaluation handling Phase 2 |
| PE8-07 | Duplicate evaluation delivery | 2 | Phase 2 (outbox `event_id` dedupe basis Planned M4) |
| PE8-08 | Retry after worker timeout | 2 | Phase 2 |
| PE8-09 | Confidence routing | 2 | Phase 2 |
| PE8-10 | Transcript revision creates new evaluation version | 2 | Phase 2 |
| PE8-11 | Unsupported claim with no source | 2 | Phase 2 |
| PE8-12 | Prompt injection in every untrusted input channel | 1–3 | Speech/text Planned (M6, M9); docs/retrieval Phase 2/3 |
| PE8-13 | Evaluator disagreement | 2 | Phase 2 |
| PE8-14 | Human override audit integrity | 5 | Phase 5 |

## Phase-0 contract tests (all Verified, `make contracts-test`)

Schema meta-validation and fixtures; state-machine structure (closure, determinism, reachability, transient chains, activation balance, INV-3/5/6/8/10, ADR-012 completeness, recording stops); doc drift (diagram ⊆ contract with ADR citations, verbatim prompts with sha256 pins, evaluation schema = document, weights across three documents, accepted ADRs unchanged, generated diagram current); isolation (Tier-1 context forbidden concepts and closed objects, no post-interview states for Tier-1, agent cannot self-attribute, ID-only events); OpenAPI (valid 3.1 with external refs, explicit security, idempotency keys, fencing on lease-bound calls, no scoring surface, problem+json).
