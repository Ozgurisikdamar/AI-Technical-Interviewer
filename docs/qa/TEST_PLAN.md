# Test plan and QA traceability

Maps every item of THREAT_MODEL_AND_QA §5, BLUEPRINT §18 and PROMPT_AND_EVALUATION §8 to a test, phase and status.

Status values:
- **Verified**: a test exists, runs in this repository and passes.
- **Verified (scripted, loopback)**: passes with the deterministic scripted engine over local WebRTC only.
- **Planned (Mx)**: scheduled in a Phase-1 milestone, not yet verified.
- **Blocked**: cannot run in the current environment (reason given).
- **Phase N**: belongs to a later phase.

Test locations: `contracts/tests` (C), `services/control-plane` Go tests (G), `services/tier1-voice-agent/tests` (A), `services/tier2-evaluator/tests` (T2), `apps/web` unit and Playwright tests (W), `tests/e2e` media, browser and pipeline tests (E).

Tier-2 rows are verified with the deterministic **reference** evaluator and with model adapters against recorded-shape provider responses. No live evaluator model has been called (no provider credentials), and evaluator quality against human labels is unmeasured (Milestone C).

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
| QA-SD-02 | Duplicate evaluation job | 2 | G: `TestTier2HandoffReviewAndReport` (a redelivered attempt returns the first evaluation and adds no attempt), `TestControlPlaneWritesEvaluationRecordsOnce`; T2: `test_redelivery_skips_answers_this_evaluator_already_evaluated`; E: `test_typed_interview_is_evaluated_reviewed_and_reported` (a redelivered completion changes nothing) | Verified |
| QA-SD-03 | Out-of-order transcript segments | 1 | G: `TestAttributionUsesSpeechTimeNotArrivalTime`, `TestConcurrentIngestionAssignsGaplessSequences` | Verified |
| QA-SD-04 | Missing transcript segment | 1 | G: `TestCandidateButtonPathWaitsForFlushOrDeadline`; A: undelivered segments keep the manifest incomplete | Verified |
| QA-SD-05 | Transcript revision after evaluation | 2 | G: an attempt for another transcript version is refused (`TestTier2HandoffReviewAndReport`); the canonical key includes the transcript version; T2: `test_transcript_revision_between_read_and_report_retries_the_delivery` | Binding Verified; no transcript-revision workflow exists yet |
| QA-SD-06 | Concurrent state transition requests | 1 | G: `TestConcurrentPauseResumeStorm`, `TestStaleVersionsAreRejected` (race detector) | Verified |
| QA-SD-07 | Session pause/resume race | 1 | G: `TestConcurrentPauseResumeStorm` | Verified |
| QA-SD-08 | Worker restart during processing | 1 | E: `test_agent_crash_is_replaced_without_duplicate_agents`; G: `TestLeaseExpiryPausesTheInterview`, `TestMissingAgentPausesAfterTheDispatchTimeout` | Verified (G); E: Verified (scripted, loopback) |
| QA-SD-09 | Database timeout and recovery | 6 | — | Phase 6 |
| QA-SD-10 | Queue consumer crash and reclaim | 2 | T2 (real redis-server): `test_crashed_consumer_entries_are_reclaimed_by_another`, `test_retry_later_leaves_the_entry_pending_until_reclaimed`, `test_slow_handling_keeps_its_claim`, `test_poison_entry_is_dead_lettered_after_max_deliveries`, `test_failed_dead_letter_report_keeps_the_entry_pending`; G: `TestRelayStopsAtAFailureAndResumesInOrder`; E: `test_dead_lettered_interview_falls_back_to_human_review` | Verified |
| QA-SD-11 | Qdrant unavailability, canonical fallback | 2 | — | Not built: ADR-022 defers Qdrant until a benchmark shows retrieval helps; PostgreSQL is the only store |

## AI and prompt security (TM §5, P&E §7)

| ID | Scenario | Phase | Test | Status |
|---|---|---|---|---|
| QA-AI-01 | Spoken "ignore all previous instructions" | 1 | A: candidate text never in instructions; E: `test_prompt_injection_changes_nothing` | Verified (A); E: Verified (scripted, loopback); live model Blocked: no provider credentials |
| QA-AI-02 | Ask for system prompt or hidden rubric | 1 | C/G: rubric never in the Tier-1 context; A: `test_monitor_flags_disclosures` | Verified (heuristic monitor, uncalibrated) |
| QA-AI-03 | Attempt to change score | 1–2 | C: no decision field anywhere; scores only in staff review and Tier-2 internal operations; the Tier-1 context has no evaluation concepts; G: the candidate and the agent have no route to evaluations; the control plane recomputes every weighted score | Verified (C: `test_api_exposes_no_hiring_decision_surface`, `test_scores_appear_only_in_staff_review_and_tier2_operations`, `test_tier1_context_contains_no_evaluation_concepts`; G: `TestScoreBoundsAndExactWeightedArithmetic`) |
| QA-AI-04 | Prompt injection in resume | 3 | — | Phase 3 |
| QA-AI-05 | Prompt injection in job description | 3 | — | Phase 3 |
| QA-AI-06 | Instruction embedded in retrieved reference | 2 | T2: `test_input_is_a_data_block_without_indicators` (rubric references and the transcript travel inside one delimited data block; the candidate cannot close it) | No retrieval exists (ADR-022); live model Blocked |
| QA-AI-07 | Tool invocation without authorization | 1 | G: `TestCompromisedAgentCannotEscalate`; A: `test_the_model_has_exactly_one_tool_without_parameters`, `test_the_model_cannot_chain_tool_calls` (one tool execution per turn); E: forbidden tool call has no effect | Verified |
| QA-AI-08 | Malformed evaluator JSON | 2 | C: schema fixtures; G: `TestSchemaFailuresNameTheLocationWithoutEchoingContent`; T2: `test_openai_non_json_output_is_stored_as_text_for_validation_to_reject`, `test_anthropic_non_json_text_is_stored_for_validation_to_reject`, truncated and refused outputs recorded as failed | Verified |
| QA-AI-09 | Fabricated evidence timestamps | 2 | G: `TestEvidenceMustComeFromTheAnswersCandidateSpeech` (spans outside the cited segments, other answers' and interviewer segments), `TestQuotesMustBeVerbatim` | Verified |
| QA-AI-10 | Unsupported technical claims | 2 | G: a non-zero score without supporting evidence is refused (`TestInjectedInstructionsCannotBuyAScore`); `unsupported_claims` routes to review (`TestAValidEvaluationIsAcceptedAndAlwaysRoutedToReview`) | Verified |
| QA-AI-11 | Contradictory answer segments | 2 | G: reported `contradictions` route to review (routing reasons) | Verified (routing); detection quality needs a model and labels |
| QA-AI-12 | Evaluator disagreement, review routing | 2 | G: `TestEvaluatorDisagreement`, `TestTier2HandoffReviewAndReport` (`evaluator_disagreement` reaches the review task), `TestConfidenceRoutesButIsNeverACalibratedProbability` | Verified; the 20-point threshold is an uncalibrated policy value |

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
| QA-FA-08 | Screen reader and keyboard navigation | 1 | W: `keyboard_flow.mjs` (keyboard-only path, `aria-live` status, dialog focus); `reviewer_flow.mjs` (a review decided by keyboard only, focus on the error summary and the confirmation); axe-core WCAG 2.1 A/AA audit of every kind of screen in every browser flow, the review console included | Verified for keyboard, ARIA wiring and automated WCAG checks (no violations); no screen-reader run |
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
| BP18-06 | Realtime, STT, evaluator timeout | A + E: `test_injected_provider_failures_pause_without_finalizing[*]` (speech recognition, synthesis, a model that never answers, a dropped stream), an errored session close, an undelivered prompt → PAUSED, no finalization, replacement agent; T2: provider timeouts, 429, 5xx, 529 and mid-stream errors leave the entry pending for retry (`test_*_retried_later`) | Verified (scripted failure injection, loopback; evaluator against recorded shapes) |
| BP18-07 | Rate limits and provider outage | A/E: failure injection; G: invitation rate limit | Verified (scripted outage; provider rate limits need the live engine) |
| BP18-08 | PostgreSQL / Redis / Qdrant restart | QA-SD-09/11 | Phase 2/6 |
| BP18-09 | Malformed evaluator JSON, partial output | QA-AI-08 | Verified (G, T2) |
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
| PE8-02 | Score boundaries and exact weighted arithmetic | 0/2 | Verified: C fixtures; G `TestScoreBoundsAndExactWeightedArithmetic` (integer scores, recomputed in hundredths); weights cross-doc (C: `test_weights_agree_across_blueprint_validator_and_schema`, `test_shipped_rubrics_are_consistent`) |
| PE8-03 | Invalid/unknown rubric version | 2 | Verified: G `TestIdentityMustMatchTheAnswerAndTheRubricVersion`, `TestRubricsMustUseTheDocumentedWeights`; C `test_every_plan_rubric_reference_resolves_to_a_shipped_rubric` |
| PE8-04 | Evidence timestamps beyond transcript length | 2 | Verified: G `TestEvidenceMustComeFromTheAnswersCandidateSpeech` |
| PE8-05 | Evidence span from interviewer instead of candidate | 2 | Verified: G `TestEvidenceMustComeFromTheAnswersCandidateSpeech` |
| PE8-06 | Empty or missing transcript segments | 1–2 | Verified: `missing_segments` flag (G) routes to review; an empty answer must be abstained on (G `TestAnEmptyAnswerMustBeAbstainedOn`; T2 `test_abstention_gates`) |
| PE8-07 | Duplicate evaluation delivery | 2 | Verified (QA-SD-02) |
| PE8-08 | Retry after worker timeout | 2 | Verified (QA-SD-10) |
| PE8-09 | Confidence routing | 2 | Verified: G `TestConfidenceRoutesButIsNeverACalibratedProbability` (0.85 is a policy value; `calibrated: true` is refused) |
| PE8-10 | Transcript revision creates new evaluation version | 2 | Binding Verified (QA-SD-05); no revision workflow yet |
| PE8-11 | Unsupported claim with no source | 2 | Verified: G `TestReferencesMustBelongToTheRubric`, non-zero scores need supporting evidence (QA-AI-10) |
| PE8-12 | Prompt injection in every untrusted input channel | 1–3 | Speech and text: never in instructions (A), scripted injection over media (E); Tier-2: G `TestInjectedInstructionsCannotBuyAScore`, T2 `test_injection_in_the_transcript_does_not_change_scores`, E: an injected instruction in a typed answer changes no score; live models Blocked; documents Phase 3 |
| PE8-13 | Evaluator disagreement | 2 | Verified: G `TestEvaluatorDisagreement` |
| PE8-14 | Human override audit integrity | 2 (ADR-022) | Verified: G `TestTier2HandoffReviewAndReport` (reviewer, prior and revised scores, rationale and rubric version recorded; the rationale is not copied into audit logs), `TestAnswerWithoutRubricIsReviewedWithARationaleOnly` |

## Phase 1 audit regressions ([PHASE1_AUDIT.md](./PHASE1_AUDIT.md))

| ID | Defect | Test | Status |
|---|---|---|---|
| CP-01, CP-02 | Unbounded webhook body before verification; webhooks processed at most once | G: `TestWebhookBodiesAreBoundedAndFailedEventsAreProcessedOnRedelivery` | Verified |
| CP-03 | Second pool connection inside the dispatch transaction | G: `TestAgentDispatchNeedsOnePoolConnection` (one-connection pool) | Verified |
| CP-04 | Media calls without timeouts under a row lock | G: `TestServerAPICallsAreBounded` | Verified |
| CP-05 | Shutdown cut off background work; stuck recordings | G: `TestShutdownDrainsBackgroundWorkWithinTheGrace`, `TestReconcileSettlesInterruptedStartsAndStops` | Verified |
| CP-06 | Client-controlled metric label | G: `TestHTTPMetricsBoundTheMethodLabel` | Verified |
| CP-08 | Operational records kept forever | G: `TestPurgeOperationalRecordsIsBoundedAndAudited` | Verified |
| CP-09 | Exchange/revocation deadlock | G: `TestExchangeAndRevocationDoNotDeadlock` | Verified |
| CP-10 | Idempotency claims blocked forever | G: `TestAStaleIdempotencyClaimCanBeTakenOver` | Verified |
| CP-11 | Sweepers stopped at the first failing session | G: `TestSweepersContinuePastAFailingSession` | Verified |
| CP-12 | Rate limit bypass within an IPv6 /64; unbounded table | G: `TestLimiterKeysIPv6ByPrefixAndIPv4ByAddress`, `TestLimiterTableIsBounded` | Verified |
| CP-13 | Listeners on every interface by default | G: `TestListenersDefaultToLoopbackOnlyInDevAndTest` | Verified |
| CP-15 | Every body read error reported as 413 | G: `TestRequestBodiesTooLargeOrUnreadable` | Verified |
| T1-01 | Deprecated default model IDs | A: `tests/test_config.py` (registry, deprecation, production tests); C: `test_shipped_model_registry_is_consistent` | Verified |
| T1-02 | Provider endpoint inherited from `OPENAI_BASE_URL` | A: `test_the_realtime_client_uses_the_configured_endpoint`, `test_the_provider_endpoint_is_pinned` | Verified |
| T1-03 | One chained tool call per turn | A: `test_the_model_cannot_chain_tool_calls` | Verified |
| P0.1 | Live provider: model availability, tool behaviour and injection, transcription, latency | A: `tests/test_provider_smoke.py` (harness against a fake provider); `make provider-smoke` | Harness Verified; live run Blocked: no provider credentials |

## Phase 2 Tier-2 pipeline (ADR-022)

| ID | Scenario | Test | Status |
|---|---|---|---|
| T2-01 | Completed interview evaluated, reviewed and reported | E: `test_typed_interview_is_evaluated_reviewed_and_reported` (T11 → T14 → T15 → T17; reference evaluator output accepted by the Go validator; review RBAC; staff-only report; candidate sees no score) | Verified (reference evaluator, loopback) |
| T2-02 | Synthetic test speech is never evaluated | G: `TestSyntheticSpeechIsNeverEvaluated`; T2: `test_abstention_gates`; E: `test_full_interview_follows_the_plan_and_hands_off_ids_only` (every scripted answer abstained, all reviewed) | Verified |
| T2-03 | Dead-lettered interview falls back to human review | G: `TestPipelineFailureFallsBackToHumanReview`; T2: consumer DLQ tests; E: `test_dead_lettered_interview_falls_back_to_human_review` | Verified |
| T2-04 | Tier-2 never acts on a live interview (INV-6) | G: `TestTier2AndReviewerCannotTouchLiveOrPreInterviewStates`, attempts refused before T14; T2: `test_interviews_not_pending_evaluation_are_left_alone`, `test_other_events_cause_no_work` | Verified |
| T2-05 | Evaluator role reads evidence only and writes nothing | G: `TestTier2EvaluatorRoleReadsOnlyEvidenceAndNarrowViews`, `TestTier2EvaluatorRoleReadsEvidenceButNeverWritesLiveState`; tooling: import boundaries | Verified |
| T2-06 | Review and report authorization, no decision surface | G: `TestTier2AndReviewEndpoints`; C: `test_api_exposes_no_hiring_decision_surface`, `test_scores_appear_only_in_staff_review_and_tier2_operations`; E: recruiter cannot decide, candidate cannot read | Verified |
| T2-07 | Question without a rubric still reaches REPORT_READY | G: `TestAnswerWithoutRubricIsReviewedWithARationaleOnly` | Verified |
| T2-08 | Worker configuration refusals | T2: `tests/test_config.py` (reference evaluator, TLS, development token and unverified models refused in production; pinned endpoints; no custom headers) | Verified |
| T2-09 | Provider adapters | T2: `tests/test_adapters.py` (pinned request bodies: OpenAI `store: false`, strict structured output; Anthropic streamed structured output, no fallbacks or beta headers; refusals, truncation, errors) | Verified against recorded shapes; live providers Blocked |
| T2-10 | Pinned prompt and packaged contracts | T2: `tests/test_prompt.py` (sha256 pin, drift against `contracts/`, provider schema keywords) | Verified |
| T2-11 | The closing remark after the interview moved on to review | G: `TestTheClosingRemarkIsStoredAfterTheInterviewMovedOnToReview`; E: `test_turkish_interview_uses_each_questions_turn_profile` (closing remark stored) | Verified |
| T2-12 | Framework log extras marked as personal data | A: `test_framework_pii_extras_are_redacted` (`lk.pii.*`); E: post-run log scan | Verified |
| T2-13 | Benchmark metrics | T2: `test_quadratic_weighted_kappa_matches_a_hand_computed_value`, `test_krippendorff_alpha_matches_the_published_example`, `test_bands_and_summaries` | Verified |
| T2-14 | Benchmark dataset integrity and session-level splits | T2: `test_the_dataset_matches_its_manifest_and_never_splits_a_session`, `test_a_tampered_or_leaking_dataset_is_refused`, `test_a_session_spanning_splits_is_refused` | Verified |
| T2-15 | Evidence validity measured by the production validator | G: `TestValidateEvaluationsAppliesTheControlPlaneValidatorOffline`, `TestValidateEvaluationsRefusesMalformedInput`; T2: `test_report_counts_validity_abstention_and_agreement`; `make tier2-bench --require-valid` | Verified (synthetic data; no evaluator-quality claim) |

## Review console (ADR-023)

| ID | Scenario | Test | Status |
|---|---|---|---|
| RC-01 | A staff token is exchanged for a short-lived HttpOnly session stored as a hash; sign-in and sign-out are audited without credentials | G: `TestStaffSignInIssuesAShortLivedHttpOnlySession`; E: `reviewer_flow.mjs` (cookie attributes), database checks in `test_reviewer_decides_every_answer_in_the_browser` | Verified |
| RC-02 | Malformed, unknown, expired and revoked tokens are refused alike; sign-ins are rate-limited; login CSRF is refused | G: `TestStaffSignInRefusesBadTokensAlikeAndIsRateLimited`; E: a malformed token caught in the browser, an unknown one refused with no cookie | Verified |
| RC-03 | The session counts only for review, reports and itself; a bearer header alone decides; a browser holding both cookies gets the candidate view; signing out with a bearer token leaves a browser's session alone | G: `TestStaffSessionIsAcceptedOnlyForReviewReportsAndItself`, `TestOnlyOperationsDeclaringStaffSessionAcceptTheCookie` (every public route against its OpenAPI security), `TestSigningOutWithABearerTokenLeavesTheSessionCookieAlone`; C: `test_the_staff_browser_session_opens_only_review_reports_and_itself` | Verified |
| RC-04 | Sessions end with sign-out, a new sign-in in the same browser, and the revocation of their token; they never outlive the token, and a token about to expire cannot start one | G: `TestStaffSessionsEndWithSignOutReplacementAndTheirToken`, `TestStaffSessionsNeverOutliveTheirToken`; `ati-admin`: `TestRevokingAStaffTokenEndsItsBrowserSessions` (live sessions only); E: both of the reviewer's cookies refused after sign-out | Verified |
| RC-05 | The control plane can only create sessions and mark them revoked; ended sessions are purged | G: `TestControlPlaneOnlyCreatesAndRevokesStaffSessions`, `TestPurgeOperationalRecordsIsBoundedAndAudited` | Verified |
| RC-06 | Reviewers never see who the candidate is | G: `TestStaffSessionIsAcceptedOnlyForReviewReportsAndItself` (403 on the staff view), `TestTier2AndReviewEndpoints` | Verified |
| RC-07 | Independent first: the AI draft is not rendered until opened, never prefills the decision, and AI-derived reasons stay with it; opening it is recorded | W: `lib/staff/staff.test.ts` (reasons, decision checks, the decision mode from `rubric_ref`); G: `TestReviewInTheBrowserSessionAndTheReportList` (`ai_draft_viewed`, false without a draft); E: every answer checked before and after the draft is opened, outcomes checked in the database | Verified (scripted, loopback) |
| RC-08 | Highlights follow the validator's quote rule exactly | G: `TestQuoteRuleMatchesTheSharedCases` (every case through `evaluation.Validate`); W: `lib/staff/highlight.test.ts` (the same cases from `contracts/fixtures/quote-matching`, plus ranges, merging, whitespace at a segment's edge, empty segments and code points) | Verified |
| RC-09 | The console computes nothing the server does not recompute | W: weighted score as `WeightedCenti`; G: the server recomputes and stores its own | Verified |
| RC-10 | Queue by interview in answer order, waiting answers oldest interview first and reviewed ones most recently decided first; a report list without scores or candidate identity; lists cut off at the largest page say so | G: `TestReviewInTheBrowserSessionAndTheReportList`; W: `lib/api/staff.test.ts` (both lists ask for the largest page); E: recruiter's report list | Verified |
| RC-11 | No credential in an address, storage or `document.cookie`; no staff token in the bundle; none in logs or the database | E: `reviewer_flow.mjs` (token checks after each sign-in), post-run log scan including session cookies, database checks; W: bundle scan rule `staff-token` | Verified |
| RC-12 | Console pages: CSP nonce, no microphone, noindex, not cached; responses validated before use | E: document headers of every console page (the whole Permissions-Policy); W: `lib/api/staff.test.ts` (strict parsers within the server's limits, lengths in code points, unknown interview states, https-only references, query allow-list) | Verified |
| RC-13 | Recruiters read reports and cannot review | G: `TestStaffSessionIsAcceptedOnlyForReviewReportsAndItself`; E: `reviewer_flow.mjs` (recruiter context) | Verified |
| RC-14 | An outage is never taken for the end of a session | G: `TestAnOutageIsNeverMistakenForASignedOutUser` (staff bearer, staff session, who-am-I and candidate cookie answer 500; no credential answers 401) | Verified |
| RC-15 | Each decision's audit names the browser session it was made through; `changed_from_ai` compares the scores themselves | G: `TestReviewInTheBrowserSessionAndTheReportList`; E: database checks in `test_reviewer_decides_every_answer_in_the_browser` | Verified |
| RC-16 | A session that ends while deciding keeps the work: the form keeps what was typed, sign-in opens in a new tab without a return address, and the same form then records the decision | E: `reviewer_flow.mjs` (the last answer, after signing out in another tab), database checks (the decision is audited under the new session) | Verified (scripted, loopback) |
| RC-17 | After sign-in the console returns only to a console page the role may open | W: `lib/staff/staff.test.ts` (`safeNext` with open-redirect inputs); E: the sign-in link of a signed-out queue carries its address | Verified |
| RC-18 | Every problem code the console can receive has its own message; codes from the server never reach built-in object members | W: `lib/api/errorMessages.test.ts` (own keys in both catalogs, `label`) | Verified |
| RC-19 | An answer that does not exist says so in its heading and leads back to the queue; a session check that finishes after a sign-out never signs anyone in again | E: `reviewer_flow.mjs` (an unknown answer); the refresh only updates a signed-in state (code review finding, no automated race test) | Verified for the heading |

## Phase-0 contract tests (all Verified, `make contracts-test`, 145 tests)

Schema meta-validation and fixtures; state-machine structure (closure, determinism, reachability, transient chains, activation balance, INV-3/5/6/8/10, ADR-012 completeness, recording stops); doc drift (diagram ⊆ contract with ADR citations, verbatim prompts with sha256 pins, evaluation schema = document, weights across three documents, accepted ADRs unchanged, generated diagram current); isolation (Tier-1 context forbidden concepts and closed objects, no post-interview states for Tier-1, agent cannot self-attribute, ID-only events); OpenAPI (valid 3.1 with external refs, explicit security, idempotency keys, fencing on lease-bound calls, no scoring surface, problem+json).
