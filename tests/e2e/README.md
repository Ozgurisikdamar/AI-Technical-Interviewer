# Media end-to-end tests

A synthetic candidate (livekit rtc participant plus the public API) runs complete voice interviews over real
WebRTC against the local stack from `tools/dev/stack.sh`: PostgreSQL 16, LiveKit OSS, the control plane and the
Tier-1 agent with the **scripted engine**.

```bash
make e2e
```

`make e2e` starts the stack, runs the tests, scans every process log and the file system, and stops the stack.
Set `ATI_E2E_REUSE_STACK=1` to run against a stack that is already up (`make stack-up`), and
`ATI_E2E_KEEP_STACK=1` to leave it running afterwards.

## What it verifies

| Test | Covers |
|---|---|
| `test_full_interview_follows_the_plan_and_hands_off_ids_only` | calibration, every plan item including the unconditional follow-up, completion, closing remark, room deletion; answers, outbox (identifiers only), session events, audit, usage |
| `test_barge_in_stops_the_interviewer_and_keeps_the_answer_open` | barge-in stop latency measured at the candidate (audio level); interrupted interviewer segments; no answer closed by a barge-in |
| `test_disconnect_pauses_and_resume_reasks_as_a_new_activation` | LiveKit webhook to PAUSED, rejoin, resume as a new activation of the same item, one answer spanning both activations |
| `test_pause_stops_audio_intake` | no candidate segment is created while paused; the interviewer stays silent |
| `test_prompt_injection_changes_nothing` | a scripted injection (and a forbidden tool call) leaves the state, the plan order and the answers untouched |
| `test_provider_failure_pauses_without_finalizing_and_resume_replaces_the_agent` | provider failure pauses without finalizing; resume dispatches a replacement agent with a new lease |
| `test_candidate_finish_button_finalizes_through_the_agent_manifest` | the candidate's button path is confirmed by the agent's flushed manifest |
| `test_agent_crash_is_replaced_without_duplicate_agents` | SIGKILL of the agent process group, pause as agent unavailable, replacement with a higher fencing token, exactly one agent in the room |
| post-run scans | no credential, JWT or transcript text in any process log; no audio file written anywhere |

## What it does not verify

- **No model provider.** The scripted engine replaces STT, LLM and TTS inside the same `AgentSession`;
  candidate intent is encoded in the pitch of synthetic tone bursts. Latency numbers are "scripted engine,
  loopback only" and say nothing about a real provider, a real network, TURN, packet loss or jitter.
- **No browser.** The web UI's own browser tests are separate (`apps/web`).
- **No recording.** Recording is off by default; egress is not available in this environment.
