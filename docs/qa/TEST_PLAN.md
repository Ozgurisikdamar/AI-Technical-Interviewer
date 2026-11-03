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
| QA-AN-01 | Microphone permission denied | 1 | W: `mic_denied_flow.mjs` (voice consented, permission refused → explained as blocked, voice cannot start → text answer finalizes), `mediaErrors.test.ts`, `controller.test.ts` (a blocked microphone is explained as blocked whatever error the browser raises) | Verified (scripted, loopback) |
| QA-AN-02 | No microphone device | 1 | W: `no_device_flow.mjs` (no capture device → "no microphone was found" → text mode), `mediaErrors.test.ts` | Verified (scripted, loopback) |
| QA-AN-03 | Device removed mid-session | 1 | W: `keyboard_flow.mjs` (capture track ends → `device_lost` pause, mic unpublished) | Verified (scripted, loopback) |
| QA-AN-04 | Browser tab suspended | 6 | — | Phase 6 |
| QA-AN-05 | 1/5/10% packet loss | 6 | — | Blocked: no `tc`/netem in this environment |
| QA-AN-06 | High jitter, bandwidth throttling | 6 | — | Blocked: no `tc`/netem |
| QA-AN-07 | TURN relay path | 6 | — | Blocked: no TURN/relay lab |
| QA-AN-08 | Reconnect after temporary disconnect | 1 | E: `test_disconnect_pauses_and_resume_reasks_as_a_new_activation`; W: `reconnect_flow.mjs` (leaving the page pauses as `network`, returning rejoins while paused and resumes as a new activation), `voice_flow.mjs` page refresh (a rejoin that beats the webhook does not pause, by design) | Verified (scripted, loopback) |
| QA-AN-09 | Candidate speaks while AI output is active (barge-in) | 1 | E: `test_barge_in_stops_the_interviewer_and_keeps_the_answer_open` | Verified (scripted, loopback) |
| QA-AN-10 | Candidate silence vs packet loss | 1 | A: `test_missing_frames_never_end_a_turn`, `test_tool_refuses_while_candidate_audio_is_degraded`; E: participant loss pauses | Verified (A); E: Verified (scripted, loopback) for participant loss; packet loss Blocked |
| QA-AN-11 | Multiple browsers and devices | 6 | — | Phase 6 (Chromium only in Phase 1) |
| QA-AN-12 | Autoplay blocked on a returning page | 1 | W: `controller.test.ts` (blocked playback reported; the enable-audio gesture unblocks it) | Verified (unit); not reproducible end to end (automated Chromium grants a user activation to scripted evaluation) |

## Media permissions and access (ADR-016, ADR-018, threat-model addendum)

| ID | Scenario | Phase | Test | Status |
|---|---|---|---|---|
| QA-MP-01 | Candidate publishes only the microphone, no data, no metadata | 1 | G: `TestCandidateTokenGrants` (decoded JWT); E: `test_the_server_enforces_microphone_only_publishing` (camera, screen share, screen-share audio refused by LiveKit; metadata not applied) | Verified (scripted, loopback); dropped data packets asserted through the server-side permission |
| QA-MP-02 | Text mode publishes nothing | 1 | G: `TestCandidateTokenGrants`; E: `test_a_text_mode_candidate_cannot_publish_audio` | Verified (scripted, loopback) |
| QA-MP-03 | Participants not minted by the control plane | 1 | G: `TestWebhooksAreVerifiedDeduplicatedAndCrossChecked`, `TestASecondAgentWhileOneHoldsTheLeaseIsRemoved`; E: `test_participants_not_minted_by_the_control_plane_are_removed` | Verified (scripted, loopback) |
| QA-MP-04 | Rejoining a deleted room with an old token | 1 | E: `test_a_withdrawn_interview_room_cannot_be_rejoined`; G: `TestPauseExpiryDeletesTheRoomAndEmitsNoCompletion` | Verified (scripted, loopback) |
| QA-MP-05 | Forged webhooks | 1 | G: `TestReceiveWebhookAcceptsOnlyTheDedicatedKey`; E: `test_forged_webhooks_are_refused` | Verified |
| QA-MP-06 | Staff revokes access | 1 | G: `TestStaffCanRevokeAnUnusedInvitation`, `TestRevokingALiveInterviewPausesItAndDeletesTheRoom` | Verified (G) |
| QA-MP-07 | Credentials never at rest or in logs | 1 | G: `TestCredentialsAreNeverStoredForReplays`, `TestTenantAndStaffTokenAreUsableAndStoredOnlyAsHashes`, startup smoke tests; E: post-run log scans | Verified |
| QA-MP-08 | Recording fails closed without an egress service | 1 | G: `TestAnUnansweredEgressStartFailsClosedWithinTheTimeout`; E: `test_recording_fails_closed_without_an_egress_service` | Verified (scripted, loopback); a real recording is unverified |

## State and data integrity (TM §5)

| ID | Scenario | Phase | Test | Status |
|---|---|---|---|---|
| QA-SD-01 | Duplicate answer event | 1 | G: `TestConcurrentCompletionFinalizesExactlyOnce`; E: one answer per item | Verified |
| QA-SD-02 | Duplicate evaluation job | 2 | — | Phase 2 |
| QA-SD-03 | Out-of-order transcript segments | 1 | G: `TestAttributionUsesSpeechTimeNotArrivalTime`, `TestConcurrentIngestionAssignsGaplessSequences` | Verified |
| QA-SD-04 | Missing transcript segment | 1 | G: `TestCandidateButtonPathWaitsForFlushOrDeadline`; A: undelivered segments keep the manifest incomplete | Verified |
| QA-SD-05 | Transcript revision after evaluation | 2 | — | Phase 2 (append-only versions in place from M3) |
| QA-SD-06 | Concurrent state transition requests | 1 | G: `TestConcurrentPauseResumeStorm`, `TestStaleVersionsAreRejected` (race detector) | Verified |
| QA-SD-07 | Session pause/resume race | 1 | G: `TestConcurrentPauseResumeStorm` | Verified |
| QA-SD-08 | Worker restart during processing | 1 | E: `test_agent_crash_is_replaced_without_duplicate_agents`; G: `TestLeaseExpiryPausesTheInterview`, `TestMissingAgentPausesAfterTheDispatchTimeout` | Verified (G); E: Verified (scripted, loopback) |
| QA-SD-09 | Database timeout and recovery | 6 | — | Phase 6 |
| QA-SD-10 | Queue consumer crash and reclaim | 2 | — | Phase 2 |
| QA-SD-11 | Qdrant unavailability, canonical fallback | 2 | — | Phase 2 |

## AI and prompt security (TM §5, P&E §7)

| ID | Scenario | Phase | Test | Status |
|---|---|---|---|---|
| QA-AI-01 | Spoken "ignore all previous instructions" | 1 | A: candidate text never in instructions; E: `test_prompt_injection_changes_nothing` | Verified (A); E: Verified (scripted, loopback); live model Blocked: no provider credentials |
| QA-AI-02 | Ask for system prompt or hidden rubric | 1 | C/G: rubric never in the Tier-1 context; A: `test_monitor_flags_disclosures` | Verified (heuristic monitor, uncalibrated) |
| QA-AI-03 | Attempt to change score | 1 | C: no score field or endpoint exists; G: no score route | Verified (C: `test_api_exposes_no_scoring_or_decision_surface`, `test_tier1_context_contains_no_evaluation_concepts`) |
| QA-AI-04 | Prompt injection in resume | 3 | — | Phase 3 |
| QA-AI-05 | Prompt injection in job description | 3 | — | Phase 3 |
| QA-AI-06 | Instruction embedded in retrieved reference | 2 | — | Phase 2 |
| QA-AI-07 | Tool invocation without authorization | 1 | G: `TestCompromisedAgentCannotEscalate`; A: `test_the_model_has_exactly_one_tool_without_parameters`; E: forbidden tool call has no effect | Verified |
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
| QA-FA-01 | Turkish and English sessions | 1 | C: en/tr plans + notices; A: `test_language_instruction_follows_the_context`, `test_openai_provider_configuration`; E: `test_turkish_interview_uses_each_questions_turn_profile` (Turkish plan end to end) | Verified (scripted, loopback); Turkish speech quality Phase 6 |
| QA-FA-02 | Code-switching | 6 | — | Phase 6 (needs consented recordings) |
| QA-FA-03 | Varied accents and speech rates | 6 | — | Phase 6 |
| QA-FA-04 | Speech disability accommodations | 1 | W: text mode; ADR-018 | Text mode Verified (scripted, loopback); broader accommodations Phase 5–6 |
| QA-FA-05 | Text-only alternative | 1 | G: `TestTextResponsesAreBoundToTheOpenQuestionAndReachTier1AsUntrustedInput`, `TestTextResponsesAreRefusedInVoiceMode`; A: `test_text_answers_reach_the_model_once_as_user_input`; W: `text_flow.mjs` | Verified (W scripted, loopback) |
| QA-FA-06 | Long thinking pauses | 1 | G/A: answers close only by the tool or the candidate's button, never by silence; A: tool refuses while the candidate speaks, `test_each_question_applies_its_turn_profile`, `test_turn_profiles_change_*` (scripted endpointing 0.3/1.2 s, OpenAI semantic VAD medium/low); E: every activation applies its profile | Verified (scripted, loopback); values uncalibrated; OpenAI profile unverified against the provider |
| QA-FA-07 | Low-quality microphone | 6 | A: `test_level_meter_flags_only_measured_quiet_turns`; E: `test_quiet_speech_is_flagged_low_energy` (quiet speech flagged `low_energy` for review, never scored) | Signal Verified (scripted, loopback); threshold uncalibrated; real microphones Phase 6 |
| QA-FA-08 | Screen reader and keyboard navigation | 1 | W: `keyboard_flow.mjs` (keyboard-only path, `aria-live` status, dialog focus); axe-core WCAG 2.1 A/AA audit of every screen in every browser flow | Verified for keyboard, ARIA wiring and automated WCAG checks (no violations); no screen-reader run |
| QA-FA-09 | No scoring on accent or vocal traits | 1–2 | C: no such fields; recordings never used for voice assessment | Verified (C) structurally |
| QA-FA-10 | Human review of disputed assessment | 5 | — | Phase 5 |

## BLUEPRINT §18 required scenarios

| ID | Scenario | Maps to | Status |
|---|---|---|---|
| BP18-01 | 1/5/10% loss, high jitter | QA-AN-05/06 | Blocked |
| BP18-02 | Mic denied, device switch | QA-AN-01/03 | Verified (W, scripted, loopback) |
| BP18-03 | Refresh and reconnect | QA-AN-08 | Verified (E and W, scripted, loopback) |
| BP18-04 | Barge-in during speech | QA-AN-09 | Verified (scripted, loopback) |
| BP18-05 | Duplicate/out-of-order transcript events | QA-SD-01/03 | Verified (G: `TestSegmentIdempotencyAndConflicts` + QA-SD-01/03) |
| BP18-06 | Realtime, STT, evaluator timeout | A + E: `test_injected_provider_failures_pause_without_finalizing[*]` (speech recognition, synthesis, a model that never answers, a dropped stream), an errored session close, an undelivered prompt → PAUSED, no finalization, replacement agent | Verified (scripted failure injection, loopback); evaluator Phase 2 |
| BP18-07 | Rate limits and provider outage | A/E: failure injection; G: invitation rate limit | Verified (scripted outage; provider rate limits need the live engine) |
| BP18-08 | PostgreSQL / Redis / Qdrant restart | QA-SD-09/11 | Phase 2/6 |
| BP18-09 | Malformed evaluator JSON, partial output | QA-AI-08 | Phase 2 |
| BP18-10 | Worker crash and retry duplication | QA-SD-08 | Verified (G; E scripted, loopback) |
| BP18-11 | Prompt injection in audio/resume/JD/retrieval | QA-AI-01/04/05/06 | Audio: Verified (scripted; live model Blocked); rest Phase 2/3 |
| BP18-12 | Unauthorized API/tool access | G: authZ, cross-tenant, compromised agent, staff token expiry/revocation, invitation revocation, expired nonces, dedicated webhook key; E: server-enforced grants, removed intruders (API-key and extra agent), forged webhooks, deleted rooms | Verified (G: `TestCandidateIsolationAndTenantScoping`, `TestAgentRequestsNeedServiceTokenGrantAndCurrentFencing`, `TestClaimRequiresRealDispatchAndAgentParticipant`, `TestCompromisedAgentCannotEscalate`, `TestStaffTokensExpireAndRevocationTakesEffectImmediately`, `TestStaffCanRevokeAnUnusedInvitation`, `TestRevokingALiveInterviewPausesItAndDeletesTheRoom`, `TestExpiredClaimNoncesAreRefused`, `TestReceiveWebhookAcceptsOnlyTheDedicatedKey`, `TestASecondAgentWhileOneHoldsTheLeaseIsRemoved`; E: `test_security.py`) |
| BP18-13 | Concurrent session creation | G: `TestConcurrentInterviewCreation` (race detector: distinct keys never merge, one key never duplicates), `TestIdempotencyKeys`, `TestCredentialsAreNeverStoredForReplays` | Verified |
| BP18-14 | Appeal after report | — | Phase 5 |
| BP18-15 | Accessibility and non-audio fallback | QA-FA-05/08 | Verified: text mode, keyboard/ARIA wiring and axe-core WCAG 2.1 A/AA audits (W); no screen-reader run |
| BP18-INV | Voice failure must not corrupt evaluation state | E: provider failure → PAUSED, no finalization | Verified (scripted, loopback) |

## PROMPT_AND_EVALUATION §8 required automated tests

| ID | Test | Phase | Status |
|---|---|---|---|
| PE8-01 | Schema valid/invalid fixtures | 0 | Verified (C: `test_schemas.py`) |
| PE8-02 | Score boundaries and exact weighted arithmetic | 0/2 | Boundaries Verified (C fixtures); arithmetic Phase 2 validator; weights cross-doc Verified (C: `test_weights_agree_across_blueprint_validator_and_schema`) |
| PE8-03 | Invalid/unknown rubric version | 2 | Phase 2 |
| PE8-04 | Evidence timestamps beyond transcript length | 2 | Phase 2 |
| PE8-05 | Evidence span from interviewer instead of candidate | 2 | Phase 2 |
| PE8-06 | Empty or missing transcript segments | 1–2 | `missing_segments` flag Verified (G); evaluation handling Phase 2 |
| PE8-07 | Duplicate evaluation delivery | 2 | Phase 2 (outbox `event_id` dedupe basis in place; a rolled-back or refused finalization leaves no outbox row: G `TestOutboxHoldsOnlyCommittedFinalizations`) |
| PE8-08 | Retry after worker timeout | 2 | Phase 2 |
| PE8-09 | Confidence routing | 2 | Phase 2 |
| PE8-10 | Transcript revision creates new evaluation version | 2 | Phase 2 |
| PE8-11 | Unsupported claim with no source | 2 | Phase 2 |
| PE8-12 | Prompt injection in every untrusted input channel | 1–3 | Speech and text: never in instructions (A), scripted injection over media (E); live model Blocked; docs/retrieval Phase 2/3 |
| PE8-13 | Evaluator disagreement | 2 | Phase 2 |
| PE8-14 | Human override audit integrity | 5 | Phase 5 |

## Phase-0 contract tests (all Verified, `make contracts-test`, 120 tests)

Schema meta-validation and fixtures; state-machine structure (closure, determinism, reachability, transient chains, activation balance, INV-3/5/6/8/10, ADR-012 completeness, recording stops); doc drift (diagram ⊆ contract with ADR citations, verbatim prompts with sha256 pins, evaluation schema = document, weights across three documents, accepted ADRs unchanged, generated diagram current); isolation (Tier-1 context forbidden concepts and closed objects, no post-interview states for Tier-1, agent cannot self-attribute, ID-only events); OpenAPI (valid 3.1 with external refs, explicit security, idempotency keys, fencing on lease-bound calls, no scoring surface, problem+json).
