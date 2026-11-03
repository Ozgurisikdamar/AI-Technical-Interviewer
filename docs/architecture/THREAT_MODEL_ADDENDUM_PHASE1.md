# Threat model addendum — Phase 1 trust boundaries

**Version:** 1.0 (proposed)
**Extends:** [THREAT_MODEL_AND_QA.md](./THREAT_MODEL_AND_QA.md) §2–3. That document is unchanged; this addendum covers boundaries introduced by the Phase 1 design (ADR-011 to ADR-020).

## 1. Additional trust boundaries

| # | Boundary | Notes |
|---|---|---|
| B10 | LiveKit dispatch metadata → Tier-1 agent | Readable by every LiveKit API key holder |
| B11 | Tier-1 agent → internal control-plane API (claim, lease, writes) | Separate listener; service token + per-session grant + fencing token |
| B12 | LiveKit server → control-plane webhook | Signed with a dedicated key held only by the control plane |
| B13 | Browser → public API via same-origin rewrite | Session cookie; Origin / Sec-Fetch-Site checks |
| B14 | Candidate text input → control plane → Tier-1 model | Untrusted; delivered as user input, never as instructions |
| B15 | Egress service → object storage (audio recordings) | Optional, fail-closed, consented, retention-bound |
| B16 | Control plane → Tier-1 context view (restricted DB role) | Allowlisted columns only; no rubric or evaluation access |

## 2. Threats, controls and verification

| Threat | Impact | Controls | Verified by |
|---|---|---|---|
| Harvesting per-session grants from dispatch metadata | Agent impersonation across sessions | Metadata holds only session ID + single-use hashed nonce; claim verified via LiveKit API; lease-bound grant | Claim and nonce-reuse integration tests |
| Zombie or duplicate agent writes after re-dispatch | Corrupted transcript, duplicate answers | Monotonic fencing token on every write; stale-lease rejection; stale agent participants removed | Fencing tests; E2E agent-crash scenario |
| Compromised or hijacked Tier-1 model | Skipped questions, fake completion, score manipulation | Single no-argument tool reaching only request R01; backend guards per transition; no score or plan tools exist | Compromised-agent sequence suite; contract INV-5/INV-8 |
| Late or misattributed transcript | Evidence under the wrong question | Server-side attribution by speech start time; answer windows open at prompt playback; unassigned/late flags | Attribution integration tests |
| Spoofed webhook (e.g., fake participant_left) | Forced pause or completion | Dedicated webhook key; event-ID dedupe; stale-event drop; ListParticipants cross-check | Webhook integration tests |
| Stale candidate token reused after withdrawal | Unauthorized rejoin | Room deleted on completion, withdrawal and expiry; `room.auto_create: false`; short initial TTL | Room-lifecycle tests; E2E `test_a_withdrawn_interview_room_cannot_be_rejoined`; G `TestPauseExpiryDeletesTheRoomAndEmitsNoCompletion`, `TestCandidateTokenGrants` (lifetime) |
| Candidate data-channel injection | Bypass of backend validation | `canPublishData=false`; agent `text_input=False`; text answers only via authenticated API | `TestCandidateTokenGrants` (decoded JWT); agent settings test; E2E `test_the_server_enforces_microphone_only_publishing` (server-side permission, refused camera and screen share, metadata not applied) |
| Invitation token leakage via logs or referrers | Account takeover | Token only in URL fragment; `Referrer-Policy: no-referrer`; exchange only on click; hashed storage (also for idempotent replays, which never repeat the token); second exchange revokes the first; staff revocation ends every candidate session and deletes a live room | API tests (`TestCredentialsAreNeverStoredForReplays`, `TestStaffCanRevokeAnUnusedInvitation`, `TestRevokingALiveInterviewPausesItAndDeletesTheRoom`); browser header checks; web tests |
| CSRF on candidate actions | Forced pause or withdraw | SameSite=Strict cookie; Origin and Sec-Fetch-Site checks against configured origin | API tests |
| Framework-level recording or log export | Unconsented audio retention | `AgentSession(record=False)`; no audio files written (E2E check); egress only through the fail-closed policy | Agent unit test; E2E filesystem check |
| Tier-1 learning evaluation outcomes | Score-aware behaviour | Post-interview states collapsed in Tier-1 context; context built through a restricted DB role | Context schema and isolation tests |
| Recording without lawful setup | Privacy breach | Every precondition required (flag, policy, consent, explicit retention, encryption attestation, voice mode); candidate track only | Recording policy matrix tests |
| Running the pre-production build with real candidates | Unlawful processing, unaccountable decisions | APP_ENV gating; production refuses draft notice, missing gate sign-offs, dev secrets, owner DB role | Config refusal tests |

## 3. Residual risks (accepted for the MVP, revisit before production)

- A compromised agent **host** holds LiveKit API credentials and can mint room tokens. Workers are treated as trusted infrastructure with a dedicated key pair, network isolation and alerts on unexpected participants. The control plane removes and audits any participant it did not mint, including a second agent that joins while another holds the lease (tested end to end); an agent-kind token that joins while no lease is held is indistinguishable from the dispatched replacement until it claims, and claiming needs the single-use nonce.
- Evidence transcription is produced inside the Tier-1 provider session and is `quality = unverified` (ADR-014).
- Speech-to-speech output cannot be validated before it is spoken. Deviation is detected post hoc (ADR-011).
- Self-hosted LiveKit does not revoke a connected participant's token on removal; room deletion is the mitigation.
