# ADR-023 — Review console: staff browser sessions and independent-first review

**Status:** Proposed — pending architecture review (both choices below are the owner's)
**Date:** 2026-11-04
**Amends:** ADR-016 (staff authentication gains a browser session)
**Relates to:** ADR-007, ADR-008, ADR-009, ADR-016, ADR-020, ADR-022; THREAT_MODEL_ADDENDUM_PHASE1

## Context

- Phase 2 routes every answer to human review and writes a report from the reviewers' decisions (ADR-022). Review and reports were reachable only through the API.
- Staff hold long-lived bearer tokens created by `ati-admin` (ADR-016; 30 days by default). A browser could use one only by keeping it where JavaScript reads it, so any script injection would take a month-long credential.
- The web app reaches the control plane only through the same-origin `/api/*` rewrite. It keeps nothing in browser storage, and every page carries a per-request CSP nonce.
- A reviewer who sees the AI's scores first tends to anchor on them (automation bias). Agreement between the evaluator and reviewers is not yet measured (ADR-022), so the human score must be the reviewer's own.
- Reviewers judge answers without knowing whose they are (ADR-022). `GET /api/v1/interviews/{id}` nonetheless returned the staff view, which names the candidate (`candidate_ref`), to every staff role.

## Decision

1. **Staff browser session.**
   - `POST /api/v1/staff/session` exchanges a pasted staff token for a session.
     - The session is an opaque 256-bit token in the cookie `ati_ss`: HttpOnly, SameSite=Strict, `Path=/api`, and Secure outside dev and test.
     - The control plane stores only its SHA-256, in `ati.staff_sessions`.
   - Lifetime:
     - The session lasts `ATI_STAFF_SESSION_TTL`: 8 hours by default, from 15 minutes to 24 hours.
     - It never outlives its staff token.
     - It ends at once when the token is revoked: every request checks the session and the token together.
   - Signing in again from the same browser revokes the session that browser presents. `POST /api/v1/staff/session/end` signs out: it revokes the session and clears the cookie.
   - Failed sign-ins:
     - A malformed, unknown, expired or revoked token gets the same 401.
     - Failed sign-ins are not audited, as with invitations.
     - Sign-ins are rate-limited per client address (`ATI_STAFF_SIGNINS_PER_MINUTE`, default 10). Behind a reverse proxy, `ATI_TRUSTED_PROXY_CIDRS` must name the proxy, or every client shares its budget and one client can use it up.
   - The sign-in request carries no bearer token, so the same-origin guard (`Origin`, `Sec-Fetch-Site`) applies to it. This also refuses login CSRF.
   - The control plane audits `staff.session.start` and `staff.session.end` under the staff token's id. The audit never holds the token, its hash or the token's label.
2. **Where the session counts.**
   - The cookie is accepted only by the review operations, the report operations and the staff session itself. These are the operations tagged `review`, plus `/api/v1/staff/session`.
   - Interview management and `GET /api/v1/interviews/{id}` keep requiring the bearer token. Interview management means creating an interview and revoking an invitation.
     - A staff member's browser may also hold a candidate cookie on the same path. Ignoring the staff cookie on these endpoints keeps the candidate view intact.
     - Interview management is an integration surface for hiring systems, not a console action.
   - When an `Authorization` header is present, it alone decides. A bad bearer token is never rescued by a cookie.
   - The principal is always the staff token. Its id is the reviewer id, the scope of idempotency keys and the audited actor, whichever credential was used.
3. **Reviewers do not learn who the candidate is.**
   - The staff view of an interview names the candidate, so only `admin` and `recruiter` may read it; reviewers get 403.
   - Review tasks and reports carry no candidate identity (ADR-022).
   - The new report list (`GET /api/v1/reports`) carries no scores and no candidate identity, so it cannot be read as a ranking.
4. **Independent first.**
   - The task page shows the question, the rubric and the candidate's transcript.
     - The passages the AI draft cites are highlighted neutrally, without dimension or assessment, so the reviewer knows where its evidence lies.
     - The highlighter applies the validator's quote rule. A shared set of cases tests both (`contracts/fixtures/quote-matching`).
   - The AI draft stays behind a disclosure button and is not rendered until the reviewer opens it. The draft is its scores, its evidence and the routing reasons derived from it. Reasons about the process (for example `no_rubric` or `pipeline_failed`) are shown with the task.
   - The draft never prefills the decision.
   - The decision records whether the draft was opened (`review_outcomes.ai_draft_viewed`), so automation bias can be studied (DATA_AND_ML_LIFECYCLE). It is recorded as false when the answer had no draft.
   - This is a nudge against automation bias, not a security boundary. The API returns the draft with the task, and a reviewer may open it at any time.
5. **Scope of the console.**
   - The console offers sign-in, the review queue, the review of an answer, the report list and the report.
   - It does not offer:
     - creating interviews or invitations;
     - administering rubrics or plans;
     - appeals (Phase 5);
     - single sign-on, which a production deployment needs instead of pasted tokens.
6. **Revocation and retention.**
   - `ati-admin revoke-staff-token --token-id` revokes a token and its sessions at once, and is audited.
   - `ati-admin list-staff-tokens` lists ids, roles, labels and expiry without any secret.
   - `ati-admin purge-operational` deletes sessions that ended before its window.

## Consequences

- A stolen session cookie is worth at most the session lifetime, and only for review and reports: it cannot create interviews or read candidate identity.
  - Script injection in the console could still act within a session, as with any cookie session. It could not read the cookie or the staff token.
  - The defences against injection stay as they were: a CSP with nonces, no inline script and no raw HTML.
- The staff token is still pasted into the browser once.
  - The field is a password field, has no `name`, is disabled until the page is ready, and is cleared after use. The token never reaches state, storage or an address.
  - Browsers may still offer to save it. Tokens issued for the console should therefore be short-lived (`create-staff-token --ttl`), and single sign-on should replace pasted tokens before production.
- Reviewers can no longer read the staff view of an interview.
- Staff have two credentials. Each operation declares which it accepts: in OpenAPI the `staffSession` scheme appears only on the allowed operations, and a contract test enforces this.

## Verification

- **Control-plane API tests:**
  - sign-in, its refusals (all alike), the rate limit and login CSRF;
  - the cookie's attributes, a lifetime bounded by the token, replacement, sign-out and revocation;
  - the cookie is accepted only where allowed; with both cookies, the browser gets the candidate view;
  - reviewers get 403 on the staff view;
  - the queue filter and its answer order, the report list without scores, and `ai_draft_viewed`.
- **Store test:** the control plane may only create sessions and mark them revoked.
- **`ati-admin` tests:** revoke, list and purge.
- **Contract tests:** the security scheme of every operation.
- **Web unit tests:** the highlighter (with the validator's cases), the weighted score, the decision checks, the parsers, the query allow-list and the split of reasons.
- **Browser end-to-end flow** (`tests/e2e/browser/reviewer_flow.mjs`):
  - headers, sign-in refusals, cookie attributes, and the token gone from the page;
  - the queue; every answer decided with the draft closed first, one by keyboard only;
  - the report, sign-out, and a recruiter who may read reports but not review;
  - an accessibility audit of every screen.
  - The test then checks the database: outcomes, sessions, audits, and no credential stored anywhere.
