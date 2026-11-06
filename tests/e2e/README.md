# Media end-to-end tests

A synthetic candidate (livekit rtc participant plus the public API) runs complete voice and text interviews over
real WebRTC against the local stack from `tools/dev/stack.sh`: PostgreSQL 16, Redis, LiveKit OSS, the control plane,
the Tier-1 agent with the **scripted engine**, and the Tier-2 worker with the deterministic **reference evaluator**.

```bash
make e2e
```

`make e2e` starts the stack, runs the tests, scans every process log and the file system, and stops the stack.
Set `ATI_E2E_REUSE_STACK=1` to run against a stack that is already up (`make stack-up`), and
`ATI_E2E_KEEP_STACK=1` to leave it running afterwards. `ATI_E2E_SCREENSHOTS=<dir>` saves screenshots of the sign-in
and review console flows.

The web UI is Turkish by default. Browser flows choose English with the `ati_lang` cookie unless they are about
Turkish (`launch({ locale })` in `browser/common.mjs`); the sign-in flow starts without a choice.

## What it verifies

| Test | Covers |
|---|---|
| `test_full_interview_follows_the_plan_and_hands_off_ids_only` | calibration, every plan item including the unconditional follow-up, completion, closing remark, room deletion; answers, outbox (identifiers only), session events, audit, usage; Tier-2 abstains on every scripted (synthetic) answer and every answer still reaches review |
| `test_typed_interview_is_evaluated_reviewed_and_reported` (`test_tier2.py`) | a typed interview through the outbox relay, Redis and the worker: T11 → T14 → T15, every attempt accepted by the control plane's validator with the expected reference scores (an injected instruction changes none), the reviewer's task view (no rubric indicators, no candidate identity), RBAC (a recruiter cannot decide, the candidate can read nothing), outcomes → T17 and the staff-only report, a candidate view without scores, and a redelivered completion that changes nothing |
| `test_dead_lettered_interview_falls_back_to_human_review` (`test_tier2.py`) | with the worker stopped, the relay's entry is replaced by a malformed one; after restart it is dead-lettered and the interview goes to review with `pipeline_failed` on every task |
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
| `test_text_mode_when_the_microphone_is_refused` (`browser/text_flow.mjs`) | voice processing declined at consent: text mode end to end, the interviewer captioned, the candidate never publishes a track |
| `test_declining_a_required_purpose_cancels_in_the_browser` (`browser/consent_decline_flow.mjs`) | declining a required purpose asks for confirmation, then cancels; every decision recorded; no outbox event; the document's security headers (nonce CSP, `frame-ancestors 'none'`, `Referrer-Policy`, `X-Frame-Options`, `nosniff`, `Permissions-Policy`) |
| `test_a_refused_microphone_falls_back_to_text` (`browser/mic_denied_flow.mjs`) | voice consented but the browser refuses the microphone: the device check explains it is blocked, voice cannot start, a written answer finalizes the first question |
| `test_no_microphone_device_falls_back_to_text` (`browser/no_device_flow.mjs`) | no capture device at all: "no microphone was found", text mode offered |
| `test_leaving_the_page_pauses_and_returning_resumes` (`browser/reconnect_flow.mjs`) | leaving the room page pauses as `network`; returning rejoins the media room while paused (no microphone published) and resumes as a new activation of the same question |
| `test_candidate_signs_in_with_an_application_number_and_access_code` (`browser/signin_flow.mjs`) | candidate sign-in (ADR-024) in Turkish, the default, on the Turkish demo plan. **Start page:** document headers; `html[lang=tr]` with no language chosen; fields without `name`, not remembered, the code hidden; switching to English and back keeps the typed number (no reload) and sets `ati_lang` (Lax, a year). **Refusals:** a malformed number sends no request; a wrong code gets 401, takes focus, marks both fields and opens no session. **Sign-in:** the right code typed in lower case with a space; an HttpOnly, SameSite=Strict cookie scoped to `/api`; the code in no address, storage, script-readable cookie or page. **Afterwards:** the notice in Turkish with the matched application (the number, a `lang="tr"` title), the steps and the TASLAK label; typed answers, setup, the room, withdrawal. An accessibility audit of every screen. **Database checks:** one `candidate.sign_in` audit and no exchange or lock; T01 once with cause `access_code_sign_in`; the counter reset; an argon2id hash with the stored parameters; the canonical number; the interview withdrawn; neither form of the code in audits, stored replays or the invitation row |
| `test_reviewer_decides_every_answer_in_the_browser` (`browser/reviewer_flow.mjs`) | the review console (ADR-023) on a typed interview evaluated by the reference evaluator. **Sign-in:** console headers (nonce CSP, no microphone, `noindex`, `no-store`); the token field has no `name`; a malformed token is caught in the browser, an unknown one is refused without a cookie; the session cookie is HttpOnly, SameSite=Strict and scoped to `/api`; the token is gone from the address, markup, inputs, storage and `document.cookie`. **Review:** for every answer, the form starts empty and the AI draft is closed and unrendered, with no AI-derived reason before it; cited passages are highlighted; an empty decision lists its problems and takes focus; opening the draft keeps focus on its button, shows the AI scores and prefills nothing; the weighted preview reads 49.00; one answer is decided by keyboard only. **Afterwards:** the report with the AI parts folded; sign-out (the old cookie is refused); a recruiter who reads reports but may not review. **Database checks:** the reviewer's own scores, with the AI scores as prior; `ai_draft_viewed` true except for the keyboard answer; T17 and one report; one session per staff member, hashed, at most 8 hours, the reviewer's revoked; start and end audits; no credential in audits or stored replays |
| accessibility (every browser flow) | axe-core 4.14.0 (pinned in `browser/package-lock.json`) audits each screen against WCAG 2.1 A/AA after transitions settle; serious or critical violations fail the flow |
| post-run scans | no credential (including the session cookies the review console and candidate sign-in flows record in the stack's state directory with mode 0600, and every access code issued during the run, in both forms), JWT or transcript text in any process log (including the Tier-2 worker's); no audio file written anywhere |

## Limits and preconditions

- **No model provider.** The scripted engine replaces STT, LLM and TTS inside the same `AgentSession`;
  candidate intent is encoded in the pitch of synthetic tone bursts. Latency numbers are "scripted engine,
  loopback only" and say nothing about a real provider, a real network, TURN, packet loss or jitter.
- **Browser flows** use the Playwright that is preinstalled for Node (`npm root -g`) and Chromium's fake microphone
  playing a pitch-coded WAV generated at test time and deleted afterwards. They are skipped when Playwright is not
  installed. axe-core is installed from `browser/package-lock.json` (`npm ci`) on first use.
- **Autoplay.** Automated Chromium grants a user activation on every scripted evaluation, so blocked autoplay (the
  enable-audio control) cannot be reproduced end to end; the web unit tests cover it. Automated accessibility checks
  are not a screen-reader test.
- **No evaluator model.** The Tier-2 worker runs the reference evaluator, which matches the synthetic rubrics'
  indicator phrases; the OpenAI and Anthropic evaluators are not exercised here.
- **No recording service.** Recording is off by default, and this environment has no egress service: the
  recording test can only show that recording fails closed. A real recording (egress, encrypted storage) is
  unverified.
- **Data packets.** The client SDK accepts `publish_data` and the server drops it; the test asserts the server-side
  permission, because a dropped packet cannot be observed without a second, legitimate subscriber.
