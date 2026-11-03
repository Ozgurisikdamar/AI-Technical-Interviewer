# Tier-1 voice agent

The realtime interviewer (ADR-001, ADR-011). It joins a LiveKit room on explicit dispatch, speaks the
questions that the control plane's flow engine approves, and forwards transcripts. It never scores, ranks or
judges answers.

## Isolation

| Guarantee | Enforced by |
|---|---|
| No database, cache, vector-store or object-storage client | `tools/checks/python_import_boundaries.py`; `test_no_storage_clients_are_installed` |
| Context only from the control plane's Tier-1 view (allowlist schema, validated before use) | `prompt.validate_context`; `test_invalid_contexts_are_rejected_before_rendering` |
| Candidate text never enters model instructions | `test_untrusted_candidate_text_never_enters_instructions` (prompt and controller) |
| One model tool, `mark_answer_complete`, with no parameters; one tool step per turn | `test_the_model_has_exactly_one_tool_without_parameters`; `test_sessions_never_select_hosted_inference` |
| Dispatch metadata is exactly `{session_id, claim_nonce}` | `test_malformed_metadata_is_refused` |
| Every session write carries the lease grant and fencing token | `test_session_requests_carry_grant_and_fencing_token` |
| Proxies are never inherited from the environment | `test_environment_proxies_are_never_used` |
| No framework recording (`record=False`), no room text input, room lifecycle owned by the control plane | `test_run_claims_and_starts_with_safe_room_settings` |
| No hosted LiveKit Inference (cloud turn detector, adaptive interruption detector), even in dev mode | `test_sessions_never_select_hosted_inference` |
| Logs redacted, including third-party loggers | `test_redaction_covers_third_party_loggers_and_extras` |

The packaged prompt and schemas under `src/tier1_voice_agent/contracts/` are copies of `contracts/`
(`tools/dev/sync-agent-contracts.sh`); the prompt's sha256 is checked at startup and a drift test compares the
copies with the sources.

## Engines

- `openai`: OpenAI Realtime through `livekit-plugins-openai` (server semantic VAD, `gpt-4o-transcribe`
  transcription). **Not verified against the live provider** in this repository: no credentials are available
  here. Turn-taking settings are uncalibrated.
- `scripted`: deterministic fake VAD, STT, LLM and TTS plugins inside the same `AgentSession`, for end-to-end
  tests without a provider. Candidate intent is encoded in the pitch of a synthetic tone burst (see
  `engines/scripted.py`), including failure injection: speech-recognition and synthesis failures, a model that never
  answers, a stream dropped mid-reply, and a score disclosure for the output monitor. Its transcripts are marked
  `synthetic_test`. It is refused in production.

Each question's turn profile is applied when it is activated: `extended_pause` gives the candidate longer before a
pause ends their turn (scripted: endpointing delay 0.3 s or 1.2 s; OpenAI: semantic VAD eagerness `medium` or
`low`). The values are uncalibrated.

## Signals and metrics

- `low_energy`: the controller keeps the received level of the candidate's microphone per frame (no audio) and flags a
  segment whose 90th-percentile level is below -35 dBFS (uncalibrated). Unmeasured segments are never flagged. It is a
  transcript-reliability signal for review, never a judgement of the candidate.
- Latency metrics come from the framework's per-reply measurements, with BLUEPRINT section 5 meanings: TTFB (first
  model token), TTFA (first synthesized audio), end of turn (candidate turn end to first reply audio) and full
  response (candidate turn end to the end of an uninterrupted reply); barge-in stop latency is measured from the
  candidate's speech onset.
- A provider failure, a voice session that closes on an error, or an approved question that could not be delivered
  pauses the interview once (`provider_failure`); a replacement agent continues after the candidate resumes.

## Completion protocol (ADR-012)

The model's tool only *requests* completion. The controller waits for a speech-free grace period on received
audio, refuses while the candidate's track is degraded, waits for transcripts of speech that already ended, flushes
them, and sends the manifest of segment keys. Segments the control plane refused for good are dropped from the
manifest; segments that could not be delivered keep the manifest incomplete, so completion fails closed and the
control plane finalizes on its deadline with `missing_segments=true`.

## Configuration

`APP_ENV` (`dev`, `test`, `staging`, `production`; anything else is refused), `ATI_VOICE_ENGINE`,
`ATI_LIVEKIT_URL`, `ATI_LIVEKIT_API_KEY`, `ATI_LIVEKIT_API_SECRET`, `ATI_AGENT_NAME`,
`ATI_CONTROL_PLANE_INTERNAL_URL`, `ATI_AGENT_SERVICE_TOKEN`, `ATI_AGENT_HTTP_PROXY` (explicit only),
`OPENAI_API_KEY`, `ATI_OPENAI_REALTIME_MODEL`, `ATI_OPENAI_VOICE`, `ATI_OPENAI_TRANSCRIPTION_MODEL`,
`ATI_AGENT_HEALTH_PORT`, `ATI_LOG_LEVEL`.

Production refuses the scripted engine, a non-https control-plane URL, a non-wss LiveKit URL and development
placeholder secrets (`ati-dev-` prefix).

## Run and test

```bash
uv sync --frozen
uv run --frozen python -m tier1_voice_agent
```

```bash
make agent-test   # from the repository root: ruff, mypy (strict), pytest
```
