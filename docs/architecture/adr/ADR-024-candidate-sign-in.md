# ADR-024 — Candidate sign-in with an application number and an access code

**Status:** Proposed — pending architecture review (the sign-in method, and keeping the invitation link beside it, are the owner's choices)
**Date:** 2026-11-06
**Amends:** ADR-016 (a candidate can also enter an interview by signing in)
**Relates to:** ADR-012, ADR-016, ADR-020, ADR-022, ADR-023; THREAT_MODEL_ADDENDUM_PHASE1; DATA_INVENTORY_AND_FLOWS

## Context

- **How candidates enter today.**
  - A candidate enters an interview only by opening the invitation link: `/i#token=…`.
  - The browser exchanges the token, on a click, for a session cookie (ADR-016).
- **Why a link is not always enough.**
  - A link can be lost, or broken by a mail client.
  - It can be opened on a different device from the one the candidate wants to use.
  - It cannot be read out or typed.
- **What the control plane knows about candidates.** It stores a candidate only as an opaque `external_ref`: no name, email or password (ADR-016). A sign-in must not change that.
- **What a candidate should see.** After signing in, the candidate should see which application the interview belongs to, before reading the notice and starting.

## Decision

1. **Two values per interview.** `POST /api/v1/interviews` issues both.
   - **Application number.**
     - 8 characters of Crockford base32, unique across interviews.
     - Shown as `XXXX-XXXX` and stored in clear on `ati.interview_sessions`.
     - It is an identifier, not a secret: on its own it opens nothing.
   - **Access code.**
     - 10 characters of Crockford base32 (50 bits), shown as `XXXXX-XXXXX`.
     - It is stored only as an argon2id hash on the invitation:
       - a PHC string with OWASP's parameters (19 MiB, 2 passes, 1 lane);
       - a random 16-byte salt;
       - a database check on the format.
   - Both are drawn from unbiased random bytes.
   - If a new number collides with an existing one, the whole creation is retried, at most 3 times.
   - Interviews created before this change have no number, so they can be entered only through the link.
2. **The code is a second credential of the same invitation.**
   - The invitation's expiry and revocation apply to the code. Revoking the invitation ends both ways in.
   - A successful sign-in behaves exactly like an exchange, through one shared code path:
     - it ends the interview's earlier candidate sessions;
     - it opens a new one under the same cookie (`ati_cs`);
     - it records the first use.
   - The first way in performs T01. Its event stays `invitation_exchanged` and its guard `invitation_valid`; only the recorded cause says which way the candidate came (`access_code_sign_in`). The state-machine contract is therefore unchanged.
   - The two ways in end each other's sessions.
   - Locks are always taken in the same order: the interview, then the invitation.
3. **Delivery.**
   - The creation response returns the invitation link, the application number and the access code. The hiring team chooses how to send them.
   - The code, like the link, is returned once. An idempotent replay returns `null` for both.
   - The staff view of an interview shows the number (to `admin` and `recruiter` only).
   - Review tasks, reports and the report list never carry it, so it cannot link an answer to a person (ADR-022).
   - The code cannot be shown again. A candidate who has lost both the code and the link needs a new interview.
4. **Sign-in.**
   - `POST /api/v1/candidate/session` takes `{application_number, access_code}`.
     - It carries no bearer token, so the same-origin guard applies. This also refuses login CSRF.
     - It needs an `Idempotency-Key`, and is never replayed from storage (a replay would store a session token).
   - **Typing is forgiving:**
     - letter case is ignored;
     - spaces, hyphens and no-break spaces are dropped;
     - `O` reads as `0`, and `I`, `L`, `ı` and `İ` read as `1`;
     - `U` is refused, and so is input over 32 characters.
     - A shared set of cases tests the control plane and the web app alike (`contracts/fixtures/sign-in-codes/cases.v1.json`).
   - **Every refusal is the same 401 `sign_in_failed`:**
     - an unknown number;
     - a wrong code;
     - an expired or revoked invitation;
     - an interview without a number;
     - a lock;
     - malformed input.
   - Past the format check (the format is public), every attempt verifies exactly one argon2 hash. For an unknown number it verifies a dummy one, so the work done does not tell the reasons apart.
5. **Guessing is bounded three ways.**
   - **Per invitation.**
     - Five wrong codes in a row lock code sign-in for 15 minutes. The lock does not extend while it lasts, and the link keeps working.
     - Each attempt is counted, in one atomic update, before its code is verified, so parallel guesses cannot exceed the limit. A success resets the count.
     - When a lock engages, it is audited (`candidate.sign_in.locked`, outcome `denied`). Single failures are not audited, as with invitations.
     - There is no unlock command: the lock ends by itself.
   - **Per client.** `ATI_CANDIDATE_SIGNINS_PER_MINUTE` (default 10) bounds the rate, separately from the other sign-ins. Behind a reverse proxy, `ATI_TRUSTED_PROXY_CIDRS` must name the proxy, or every client shares one budget.
   - **Per process.**
     - At most 4 verifications run at once, each holding about 19 MiB.
     - A sign-in waits at most 3 seconds for a slot, then gets 503 `unavailable`. Such an attempt is not counted against the invitation.
6. **What the candidate sees.**
   - The candidate's view of the interview adds:
     - `application_number`, as shown;
     - `interview_title`, the plan's title, which is now visible to candidates;
     - `language`, the plan's language, so pages can mark the title and the questions with it.
   - A successful sign-in is audited as `candidate.sign_in`. The audit never holds the number or the code.

## Consequences

- **Online guessing.** Five guesses per 15 minutes is at most 480 a day per invitation. Over the default 72-hour invitation lifetime that is 1,440 guesses against 2^50 codes, a chance of about 1 in 7.8 × 10^11.
- **Offline guessing.** A copy of the database yields only argon2id hashes. Each guess costs 19 MiB of memory and two passes.
- **Lockout as denial of service.** Anyone who knows a number can keep code sign-in locked, 15 minutes at a time. The link is not affected, and the lock never extends itself.
- **Timing.** An unknown number skips the counting update, so whether a number exists may be measurable by timing. Numbers are identifiers, not secrets, and the code is still needed.
- **Plan titles.** Titles become visible to candidates, so a plan's title must not carry internal notes.
- **Memory.** The control plane needs about 76 MiB of headroom for concurrent verifications.
- **Older interviews.** Interviews created before this change can be entered only through the link.
- **A credential the browser handles.** The code is typed into a page.
  - The field is a password field without a `name`, so the code is not sent with an ordinary form submission.
  - It is disabled until the page is ready.
  - It is cleared after use.
  - The code never reaches the page's state, storage, an address or the logs.
  - Pasting is allowed (WCAG 2.2, SC 3.3.8).

## Verification

- **Service tests** (`internal/interview/signin_test.go`):
  - only the hash is stored;
  - a sign-in opens a session like an exchange (T01 once, the audit, the counter reset), and normalised variants are accepted;
  - sign-in and the link end each other's sessions;
  - every refusal performs exactly one verification, and a malformed one none;
  - five wrong codes lock for 15 minutes, without extending, and the lock is audited once while the link still works;
  - twenty parallel wrong guesses engage exactly one lock;
  - a busy refusal is not counted;
  - sign-in racing revocation never deadlocks, and no session survives the revocation;
  - collisions are retried;
  - the view fields are set.
- **Package test** (`internal/accesscode`):
  - normalisation against the shared cases;
  - formats;
  - hash and verify;
  - the pinned OWASP prefix;
  - malformed or costly hashes never match.
- **API tests** (`internal/api/signin_test.go`, `access_test.go`):
  - the cookie is the same as the link's, and Secure outside dev and test;
  - every refusal has an identical body and sets no cookie;
  - login CSRF, a missing Idempotency-Key, unknown fields, a non-JSON body and an oversized body are refused;
  - the rate limit holds;
  - the code is never returned on replay, stored, audited or logged;
  - recruiters see the number, and reviewers never do.
- **Store tests:** neither the Tier-1 nor the Tier-2 database role can read invitations.
- **Config and contract tests:** the new setting and its bounds; the unauthenticated operation and its schemas in OpenAPI.
- **Browser end-to-end flow:** a candidate signs in with the number and code, sees the matched application, reads and answers the notice, and starts the interview.
