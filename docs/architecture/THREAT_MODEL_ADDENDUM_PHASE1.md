# Threat model addendum — Phase 1 trust boundaries

**Version:** 1.0 (proposed)
**Extends:** [THREAT_MODEL_AND_QA.md](./THREAT_MODEL_AND_QA.md) §2–3. That document is unchanged; this addendum covers boundaries introduced by the Phase 1 design (ADR-011 to ADR-020), since Milestone D, the review console (ADR-023), and, since Milestone E, candidate sign-in (ADR-024).

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
| B17 | Staff browser → public API (review console, ADR-023) | A pasted staff token is exchanged once for a session cookie; the cookie counts only for review, reports and the session itself |
| B18 | Candidate browser → public API (sign-in, ADR-024) | An application number and an access code are exchanged once for the candidate session cookie; the code is a second credential of the invitation |

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
| Staff token pasted into the console leaks (URL, page, storage, logs, a native form submission before hydration) | A month-long credential exposed | The field has no `name`, is disabled until the page hydrates, is a password field and is cleared after use; the token is sent only in a same-origin POST body; nothing is stored in the browser; the server stores hashes; bundle scan rule for staff tokens; log scans | E2E `reviewer_flow.mjs` (token absent from the address, markup, inputs, storage and `document.cookie`), post-run log scan, database checks |
| Stolen review console session | Reviews or reports read or forged | HttpOnly, SameSite=Strict, `Path=/api` cookie stored as a hash; 8 hours at most and never longer than the token; revoked by sign-out, a new sign-in and token revocation; accepted only by review, report and session operations, never by interview management or candidate operations | G `TestStaffSession…`; E: old cookie refused after sign-out |
| Login CSRF (a victim's browser signed in to an attacker's account) | Reviews attributed to the wrong staff member | The sign-in carries no bearer token, so the same-origin guard applies (Origin, Sec-Fetch-Site) | G `TestStaffSignInRefusesBadTokensAlikeAndIsRateLimited` |
| CSRF on review decisions and sign-out | Forged decisions | SameSite=Strict cookie; Origin and Sec-Fetch-Site checks on every cookie-authenticated change; an `Authorization` header alone decides when present | G `TestReviewInTheBrowserSessionAndTheReportList`, `TestStaffSessionsEndWithSignOutReplacementAndTheirToken` |
| Guessing staff tokens through the sign-in | Account takeover | 256-bit tokens; per-client rate limit (`ATI_STAFF_SIGNINS_PER_MINUTE`); identical refusals for every failure | G rate-limit test |
| A staff cookie confused with a candidate's in one browser | Wrong view or action | Candidate operations read only `ati_cs`; the staff view of an interview requires the bearer token | G: both cookies give the candidate view |
| Reviewer learns who the candidate is | Biased review | Reviewers are refused the staff view of an interview (it names the candidate); tasks, reports and the report list carry no candidate identity | G `TestStaffSessionIsAcceptedOnlyForReviewReportsAndItself`, `TestTier2AndReviewEndpoints` |
| Automation bias: the reviewer copies the AI draft | AI judgement presented as human review | The draft is not rendered until opened and never prefills the decision; AI-derived reasons stay with it; opening it is recorded for study | E `reviewer_flow.mjs`; G `ai_draft_viewed` |
| Online guessing of access codes | Another candidate's interview taken over | 50-bit codes; five wrong codes in a row, each within 15 minutes of the last, lock code sign-in for 15 minutes, counted before the code is verified, so parallel guesses cannot exceed the limit; a per-client rate limit; the invitation's expiry | G `TestFiveWrongCodesLockCodeSignInForAWhile`, `TestParallelGuessesCannotExceedTheLimit`, `TestCandidateSignInIsRateLimitedPerClient` |
| A candidate locked out by their own typos or a failed sign-in | A rightful candidate refused for 15 minutes | Wrong codes further apart than the lock period start the count again; a right code whose session cannot open undoes what it counted; the invitation link is never locked | G `TestWrongCodesFarApartDoNotAddUp`, `TestARightCodeWhoseSessionCannotOpenIsNotHeldAgainstTheCandidate` |
| Every candidate sharing one sign-in budget behind the web app's proxy | Sign-in refused site-wide after a few attempts | Clients are told apart through `ATI_TRUSTED_PROXY_CIDRS`; production refuses to start without it | G `TestProductionRefusalReasons` |
| Offline guessing after a database leak | Codes recovered | argon2id (19 MiB, 2 passes, 1 lane) with a random salt; parameters read from stored hashes are bounded | `internal/accesscode` tests |
| Telling unknown numbers from wrong codes | Interviews enumerated | One identical 401 for every refusal; exactly one argon2 verification per attempt past the format check, against a dummy hash (made at start-up) for unknown numbers; one database statement looks the attempt up and counts it | G `TestEveryRefusedSignInLooksTheSame`, `TestCandidateSignInRefusesEveryBadAttemptAlike` |
| Exhausting memory through argon2 | Control plane unavailable | At most 4 verifications at once; a sign-in waits 3 seconds for a slot, then gets 503 without its attempt being counted; a slot is held only while an attempt is looked up, counted and verified, never while its session opens | G `TestBusySignInsAreRefusedWithoutCountingAnAttempt`, `TestASessionLockedElsewhereHoldsNoVerificationSlot` |
| Login CSRF on candidate sign-in | A victim's browser enters an interview chosen by the attacker | The sign-in carries no bearer token, so the same-origin guard applies (Origin, Sec-Fetch-Site); SameSite=Strict cookie | G `TestCandidateSignInRefusesEveryBadAttemptAlike` |
| Access code leakage (address, page, storage, logs, audits, replays) | Interview taken over | The code goes only in a same-origin POST body; the fields have no `name`, are disabled until the page hydrates and are cleared after use; nothing is stored in the browser; hashed at rest; never in a replay, an audit or a log | G `TestCredentialsAreNeverStoredForReplays`; E `signin_flow.mjs`, the log scan of short secrets, database checks |
| Reviewer learns the candidate through the application number | Biased review | Only admins and recruiters see the number; review tasks, reports and the report list never carry it; the Tier-1 and Tier-2 roles cannot read it | G `TestTheApplicationNumberIsShownToRecruitersButNeverInReview`, store role tests |
| A language switch loses what is held only in memory | Lost invitation, typed answers or scores | The switch re-renders from the server without reloading the page | E `signin_flow.mjs` (a typed value survives both switches) |
| Running the pre-production build with real candidates | Unlawful processing, unaccountable decisions | APP_ENV gating; production refuses draft notice, missing gate sign-offs, dev secrets, owner DB role | Config refusal tests |

## 3. Residual risks (accepted for the MVP, revisit before production)

- A compromised agent **host** holds LiveKit API credentials and can mint room tokens. Workers are treated as trusted infrastructure with a dedicated key pair, network isolation and alerts on unexpected participants. The control plane removes and audits any participant it did not mint, including a second agent that joins while another holds the lease (tested end to end); an agent-kind token that joins while no lease is held is indistinguishable from the dispatched replacement until it claims, and claiming needs the single-use nonce.
- Evidence transcription is produced inside the Tier-1 provider session and is `quality = unverified` (ADR-014).
- Speech-to-speech output cannot be validated before it is spoken. Deviation is detected post hoc (ADR-011).
- Self-hosted LiveKit does not revoke a connected participant's token on removal; room deletion is the mitigation.
- Review console (ADR-023):
  - Staff paste a long-lived token into the browser once, and a browser may offer to save it. Console tokens should be short-lived, and single sign-on should replace pasted tokens before production.
  - Script injection in the console could act within a session, as with any cookie session; it cannot read the cookie or the token. The CSP with nonces is the defence.
  - The sign-in rate limit is per client address. Behind an unconfigured reverse proxy, every client shares the proxy's budget (`ATI_TRUSTED_PROXY_CIDRS`).
  - Independent first is a nudge, not a control: a reviewer can open the AI draft at any time.
- Candidate sign-in (ADR-024):
  - Whether an application number exists may be measurable by timing: an unknown number skips one database update. Numbers are identifiers, not secrets, and the code is still needed.
  - Anyone who knows a number can keep code sign-in locked, 15 minutes at a time. The invitation link still works, and there is no unlock command.
  - Clients are told apart by address, through the web app's proxy: every user behind one shared address (an office network, a carrier-grade NAT) shares one sign-in budget.
  - The code is valid as long as the invitation (72 hours by default) and is typed into a page, where a compromised device or someone watching can read it.
  - The language cookie can be read by script; it holds only `tr` or `en`.
