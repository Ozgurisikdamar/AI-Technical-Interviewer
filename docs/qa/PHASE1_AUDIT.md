# Phase 1 audit (2026-11-04)

This audit checks the handover findings against the code, together with an independent review of the control plane, the Tier-1 agent and the dependencies.

- **Audited revision:** `dfddd8f`. Line references point to that revision.
- **Fixes:** in the commit that adds this report.
- **How each finding was confirmed:** a regression test that fails on the audited code and passes after the fix. CP-07 and CP-14 are the exceptions; the table states how they were checked.

Severity is the impact on candidates, data or availability if the defect were triggered in a deployment. "Reproduced" means the defect was observed by running the old code. "Confirmed by reading" means the defect is established from the code, and the regression test proves only that the fixed behaviour holds.

## Control plane (`services/control-plane`)

| ID | Severity | Finding (audited location) | Impact | Fix | Evidence |
|---|---|---|---|---|---|
| CP-01 | High | The LiveKit webhook body was read without a size limit, before its signature was verified. The verifier hashes the whole body (`internal/api/internal.go:276-277`; `livekit/protocol/webhook/verifier.go`) | Anyone who can reach the internal listener can exhaust memory without credentials | `http.MaxBytesReader` (1 MiB) before verification; 413 for oversized bodies | Reproduced: `TestWebhookBodiesAreBoundedAndFailedEventsAreProcessedOnRedelivery` (`internal/api/api_test.go`) returns 401 instead of 413 on the old code |
| CP-02 | Medium | Webhooks were processed at most once. The dedupe row was committed before processing, and the handler returned 200 even when processing failed (`internal.go:284-301`) | A failed `participant_left` or egress event is lost for good, because LiveKit only retries 5xx. Pauses and recording states are then missed | An event counts as a duplicate only once `processed_at` is set (migration 00006). Processing errors return 503; a concurrent delivery gets `in_flight`, also 503 | Same test: 503, then processed on redelivery, then a duplicate |
| CP-03 | Medium | Pool deadlock. `EnsureAgentDispatch` holds a transaction and the session row lock, while `removeStaleAgents` takes a second pool connection through `ActiveAgentIdentity` (`internal/interview/dispatch.go:55-81`, `internal/api/public.go:376-379`) | Concurrent joins or resumes can exhaust the pool (30 connections) and block until their contexts end | The lease holder is read inside the transaction and passed to `RemoveStaleAgents`; the extra query is removed | Reproduced: `TestAgentDispatchNeedsOnePoolConnection` never finishes on the old code (killed at 120 s) |
| CP-04 | Medium | Media calls were made while holding the session row lock, without timeouts (`dispatch.go:76-81`, `internal/realtime/realtime.go`), and without an idle-in-transaction limit (`internal/store/store.go`) | A slow or unreachable LiveKit holds the row lock, and with it every transition of that session | Per-call timeout of 5 s on every server API call (`realtime.DefaultCallTimeout`); `idle_in_transaction_session_timeout` of 30 s as a backstop | `TestServerAPICallsAreBounded` (`internal/realtime`) |
| CP-05 | Medium | Shutdown waited only for the HTTP servers. The sweepers, the asynchronous recording start and their effects were cut off (`cmd/control-plane/main.go:111,123-128`, `internal/api/server.go:197-200`). Recordings left `starting` or `stopping` were never reconciled | A restart during a recording start can leave a recording that is untracked or stuck. A stop that fails leaves the egress running | A tracked `api.Background`. `drain` waits within a 20 s grace for servers, sweepers and background work. `Recorder.Reconcile` stops any egress still writing a stale start's file, fails the recording (audited), and retries stale stops. It runs at start-up and every 30 s | `TestShutdownDrainsBackgroundWorkWithinTheGrace`, `TestReconcileSettlesInterruptedStartsAndStops` |
| CP-06 | Medium | The `method` metric label came straight from the client (`internal/api/metrics.go:54`) | Unbounded Prometheus series from arbitrary methods | Methods outside the standard set are counted as `other` | Reproduced: `TestHTTPMetricsBoundTheMethodLabel` (the client's methods appear as labels on the old code) |
| CP-07 | Medium | Missing indexes: `pause_intervals` and `audio_recordings` by session and start time (looked up per ingested segment, `internal/interview/ingest.go:136,211`); pending `agent_dispatches`; the missing-agent sweeper's live states; lease expiry | Sequential scans on every segment and every sweep as the tables grow | Migration 00006 adds five indexes. In production, create them with `CREATE INDEX CONCURRENTLY` | Checked by hand: the down-and-up migration round trip succeeds, and `EXPLAIN` uses each index |
| CP-08 | Low | `webhook_events` and `idempotency_records` grew without bound. Replays keep response bodies, which hold candidate references | Unlimited growth; personal data kept indefinitely | `ati-admin purge-operational [--older-than 24h]` (minimum 1 h), audited. The control plane cannot delete rows | `TestPurgeOperationalRecordsIsBoundedAndAudited` (`cmd/ati-admin`) |
| CP-09 | Low | Lock-order inversion: exchange locked the invitation, then the session; revocation locked the session, then the invitation (`internal/interview/ops.go:116-127` vs `revoke.go:28-36`) | A concurrent exchange and revocation deadlock. PostgreSQL aborts one of them with 40P01 | Exchange locks the session first, then re-reads the invitation `FOR UPDATE` | Reproduced: `TestExchangeAndRevocationDoNotDeadlock` fails on the old code with `deadlock detected (SQLSTATE 40P01)` in its first iterations |
| CP-10 | Low | Idempotency bookkeeping used the request context, and a claim without an outcome blocked its key forever (`internal/api/idempotency.go:146-198`) | A client that disconnects, or a process that crashes, leaves the key "in progress" permanently | The outcome is written with a detached 5 s context. A claim older than 5 minutes (`claimed_at`, migration 00006) can be taken over | Confirmed by reading; `TestAStaleIdempotencyClaimCanBeTakenOver` covers the takeover |
| CP-11 | Low | Each sweeper stopped at the first session that failed (`completion.go:209-211,257-259,397-399`, `dispatch.go:233`) | One broken session blocks deadline, lease, pause-expiry and missing-agent handling for all others | The sweeper continues and returns the joined errors; the loop logs them and still counts the sessions it processed | Reproduced: `TestSweepersContinuePastAFailingSession` (the old code processes none of the other sessions) |
| CP-12 | Low | The rate limiter keyed IPv6 clients by full address (/128) in an unbounded map, pruned only when it grew large | Rotating addresses within a /64 bypasses the limit and grows the map | IPv6 clients are keyed by their /64. The table is an LRU of 10,000 entries | `TestLimiterKeysIPv6ByPrefixAndIPv4ByAddress`, `TestLimiterTableIsBounded` |
| CP-13 | Low | Both listeners defaulted to every interface (`:8080`, `:8081`; `internal/config/config.go:164-165`) | The internal API (agent credentials, webhooks) is exposed by default | dev and test default to loopback; staging and production must name their listeners | Reproduced: `TestListenersDefaultToLoopbackOnlyInDevAndTest` (the old defaults were `:8080` and `:8081`) |
| CP-14 | Low | `WriteTimeout` of 30 s was below the slowest bounded handler: up to six media calls during an agent dispatch, or an egress start during a microphone webhook | A slow media server drops the connection instead of returning an error response | `WriteTimeout` of 60 s, with the bound documented next to the constant | Checked by reading: a timing bound, not a behaviour test |
| CP-15 | Low | `readBody` answered 413 to every read error (`idempotency.go:75-78`) | Truncated or aborted bodies are misreported as too large | 413 only for `MaxBytesError`; 400 otherwise | `TestRequestBodiesTooLargeOrUnreadable` (reproduced: 413 on the old code) |

Reviewed and found to be correct:
- the webhook signature uses a dedicated key, and webhook age is bounded;
- the candidate token is limited to microphone publishing (also tested end to end);
- outbox events are written in the transaction of the transition that emits them;
- idempotent replays never store credentials;
- the control plane role has no DELETE.

## Tier-1 agent (`services/tier1-voice-agent`)

| ID | Severity | Finding | Impact | Fix | Evidence |
|---|---|---|---|---|---|
| T1-01 | Medium | Default model IDs `gpt-realtime` and `gpt-4o-transcribe` (`src/tier1_voice_agent/config.py:81,83`) have announced shutdowns: 2027-01-20 and 2027-02-26 ([OpenAI deprecations](https://developers.openai.com/api/docs/deprecations)) | The agent would stop working at the shutdown date, with no warning beforehand | The model registry (`contracts/models/model-registry.v1.json`). The openai engine has no defaults: unknown, retired or deprecated models are reported or refused by environment, and production requires a verified model (ADR-021) | `tests/test_config.py` (registry, deprecation and production tests); contract test `test_shipped_model_registry_is_consistent` |
| T1-02 | Low | No `base_url` was passed, so the realtime client falls back to `OPENAI_BASE_URL` (`livekit/agents/llm/_realtime/openai.py:543`) | One inherited environment variable sends the provider key and the candidate audio to another host | `ATI_OPENAI_BASE_URL` is passed explicitly. A different `OPENAI_BASE_URL` is refused. https is required (http only for a local endpoint in dev and test), and production uses only the pinned endpoint | `test_the_realtime_client_uses_the_configured_endpoint`, `test_the_provider_endpoint_is_pinned` |
| T1-03 | Low | `MAX_TOOL_STEPS = 1` permits one chained tool call. livekit-agents counts steps from 1 (`voice/agent_activity.py:4071,4833`) | The model can call `mark_answer_complete` twice in one turn. The server state check limits the effect | `MAX_TOOL_STEPS = 0`: the reply to a tool result uses `tool_choice="none"`. The framework's resulting warning is filtered | Reproduced: `tests/test_tool_steps.py` counts 2 executions on the old value |
| T1-04 | Not verifiable here | Pairing of transcription completions that arrive out of order with turns (`controller._take_turn`) depends on provider event ordering | Possible misattribution of a late transcript | Not changed | Needs the provider: belongs in the `make provider-smoke` follow-up |
| T1-05 | Medium (found during Milestone B) | livekit-agents warns with the candidate's transcript in the log extra `lk.pii.user_input` (`voice/agent_activity.py`, e.g. "skipping reply to user input, current speech generation cannot be interrupted"). The redaction filter matched key names such as `transcript` and `text`, but not that one | The candidate's words were written to the agent log whenever they ended a turn the agent could not answer, such as during the closing remark | Every extra key containing `pii` is redacted: the framework's own personal-data marker | `test_framework_pii_extras_are_redacted`; caught by the E2E log scan (`tests/e2e/conftest.py`) |

## Dependencies (`make audit`, 2026-11-04)

| Scanner | Scope | Result |
|---|---|---|
| govulncheck v1.9.0 | Control plane | No vulnerable symbol reached. `golang.org/x/net` was upgraded to v0.62.0 (GO-2026-6866, GO-2026-6860) and `github.com/google/cel-go` to v0.30.0 (GO-2026-6094); none of these was reached. GO-2026-5932 (`golang.org/x/crypto` v0.58.0, not reached) has no tagged release with the fix yet |
| pip-audit 2.9.0 | `services/tier1-voice-agent`, `contracts`, `tests/e2e` lockfiles | No known vulnerabilities |
| pnpm audit | `apps/web` production dependencies | No known vulnerabilities |

Not done: SBOM generation, licence review, container image scanning (no images are built), and secret scanning beyond the repository hygiene check. These stay with Milestone F.

## Full-duplex assessment

The architecture is turn-based with barge-in, not full duplex. The interviewer neither speaks over the candidate nor backchannels. A duplex model client exists in the installed plugin, but it conflicts with the verbatim-prompt, per-question-instruction, single-tool and pause invariants. ADR-021 records the decision and the benchmark that a full-duplex prototype would require.

## Verification of the fixes

The commits that carry these fixes record what was run and the results:
- `make test`;
- a fresh `make e2e`;
- `make audit`;
- `make provider-smoke`, which returns BLOCKED because there is no key.

These are recorded in the [implementation status](../status/IMPLEMENTATION_STATUS.md).
