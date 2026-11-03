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
| `test_voice_interview_in_the_browser` (`browser/voice_flow.mjs`) | the web UI in Chromium: invitation fragment removed before the click, DRAFT notice and pre-production banner, consent, device check, live room, pause unpublishes the microphone on the media server, refresh rejoins, completion; no CSP violation, no internal call, every POST idempotent |
| `test_keyboard_only_with_device_loss_and_withdrawal` (`browser/keyboard_flow.mjs`) | keyboard-only consent, setup and room controls; ARIA live announcements; microphone loss pauses (`device_lost`) and stops publishing; keyboard resume; withdrawal dialog focuses the safe choice; withdrawal leaves the room |
| `test_injected_provider_failures_pause_without_finalizing[*]` | speech-recognition failure, speech-synthesis failure, a model that never answers (the stream enforces its timeout) and a stream dropped mid-reply: each pauses with `provider_failure`, finalizes nothing, and a replacement agent continues |
| `test_quiet_speech_is_flagged_low_energy` | a quiet answer fragment (about -38 dBFS) is transcribed and flagged `low_energy`; the flag reaches the finalized answer's integrity flags |
| `test_latency_metrics_and_policy_flags_reach_the_control_plane` | the agent's TTFB, TTFA, end-of-turn and full-response histograms appear on `/metrics`; a scripted score disclosure is flagged by the output monitor and stored as a `policy_flag` integrity event |
| `test_turkish_interview_uses_each_questions_turn_profile` | the Turkish demo plan end to end (Turkish greeting, prompts and closing); every activation applies its turn profile (`extended_pause` for q2) |
| `test_recording_fails_closed_without_an_egress_service` | recording enabled with every precondition declared, tenant opt-in through `ati-admin` and candidate consent: LiveKit never answers the egress start, the recorder's timeout marks the recording failed and audits it, nothing reports recording, no audio exists, and the interview continues |
| `test_the_server_enforces_microphone_only_publishing` | the server-side grant is microphone only, no data, no metadata; camera, screen share and screen-share audio publications are refused by the server; metadata changes are not applied |
| `test_a_text_mode_candidate_cannot_publish_audio` | a text-mode token can publish nothing, enforced by the server |
| `test_participants_not_minted_by_the_control_plane_are_removed` | a participant with an API-key token and a second agent-kind participant (while the dispatched agent holds the lease) are removed at once and audited (`media.unexpected_participant`) |
| `test_a_withdrawn_interview_room_cannot_be_rejoined` | withdrawal deletes the room; the old media token cannot rejoin it (`room.auto_create: false`) and no new token is issued |
| `test_forged_webhooks_are_refused` | unsigned webhooks and webhooks signed with the API key (not the dedicated webhook key) are refused and not recorded |
| post-run scans | no credential, JWT or transcript text in any process log; no audio file written anywhere |

## Limits and preconditions

- **No model provider.** The scripted engine replaces STT, LLM and TTS inside the same `AgentSession`;
  candidate intent is encoded in the pitch of synthetic tone bursts. Latency numbers are "scripted engine,
  loopback only" and say nothing about a real provider, a real network, TURN, packet loss or jitter.
- **Browser flows** use the Playwright that is preinstalled for Node (`npm root -g`) and Chromium's fake microphone
  playing a pitch-coded WAV generated at test time and deleted afterwards. They are skipped when Playwright is not
  installed.
- **No recording service.** Recording is off by default, and this environment has no egress service: the
  recording test can only show that recording fails closed. A real recording (egress, encrypted storage) is
  unverified.
- **Data packets.** The client SDK accepts `publish_data` and the server drops it; the test asserts the server-side
  permission, because a dropped packet cannot be observed without a second, legitimate subscriber.
