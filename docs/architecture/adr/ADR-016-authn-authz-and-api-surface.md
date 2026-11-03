# ADR-016 — Authentication, authorization, agent leases and API surface

**Status:** Proposed — pending architecture review
**Date:** 2026-11-03
**Relates to:** BLUEPRINT §3.1, §9, §17; THREAT_MODEL_AND_QA §2–3; OpenAPI `docs/contracts/openapi/control-plane.v1.yaml`

## Context

BLUEPRINT §17 lists an initial API but no consent, invitation, answer-completion, agent or webhook endpoints, and no authentication model. Several threats are specific to LiveKit:

- Every holder of a LiveKit API key, including agent workers, can mint room tokens and read dispatch metadata.
- Connected clients' tokens are refreshed by the server, and on self-hosted deployments removing a participant does not revoke the token.
- Webhooks are signed with whichever key the server is configured to use.

## Decision

1. **Principals.**
   - *Staff*: opaque bearer tokens, stored as SHA-256 hashes, with expiry, role (`admin`, `recruiter`) and tenant. Bootstrapped by CLI.
   - *Candidate*: a single-use invitation token delivered in the URL **fragment**, so it never appears in request lines or server logs. It is exchanged only on an explicit user click, which protects against link scanners. The exchange returns an opaque session token in an `HttpOnly; Secure; SameSite=Strict` cookie, scoped to exactly one interview. A second exchange revokes the first session.
   - *Tier-1 agent*: see 3.
2. **Object-level authorization on every request:** tenant scoping for staff; a candidate can only reach their own interview, and other IDs return 404 to prevent enumeration. Candidate and staff receive different response shapes. Mutating public endpoints check `Origin` and `Sec-Fetch-Site` against the configured public origin. `X-Forwarded-For` is trusted only from configured proxy addresses. Idempotency keys are scoped per principal.
3. **Agent claim and lease.**
   - Server-side dispatch metadata contains only `{session_id, claim_nonce}`. The single-use nonce is stored hashed and expires.
   - The agent claims the session with its service token, the nonce and its job ID. The control plane verifies the dispatch and participant through the LiveKit API, then issues a **lease**: a per-session grant (stored hashed), a monotonically increasing **fencing token**, and a TTL renewed by heartbeat.
   - Every internal write carries the grant and fencing token. A stale token is rejected, so a replaced or zombie agent cannot write.
   - When a lease expires, the session moves to PAUSED (agent unavailable). Re-dispatch checks existing dispatches first and removes stale agent participants.
4. **LiveKit.**
   - Rooms have opaque names, are created only by the control plane (`room.auto_create: false`), and are deleted on completion, withdrawal or expiry.
   - The candidate token lasts about 10 minutes, has a fixed identity `candidate`, allows publishing only from the microphone (and nothing in text mode), and sets `canPublishData=false` and `canUpdateOwnMetadata=false`.
   - Webhooks are signed with a **dedicated key held only by the control plane**. They are deduplicated by event ID, stale events are dropped, and every action is cross-checked with `ListParticipants`.
5. **Two listeners.** The public listener serves `/api/v1` behind the same-origin web rewrite. The internal listener serves `/internal/v1` (Tier-1 API and the LiveKit webhook), `/healthz`, `/readyz` and `/metrics`, and the rewrite cannot reach it. Outside dev/test the agent must use TLS to reach it.
6. **API additions to BLUEPRINT §17** (all versioned, documented in OpenAPI):
   - Public: invitation exchange, consent notice and consent, calibration-complete, answer-complete (a request), withdraw, text-responses (ADR-018).
   - Internal: claims, lease renew and release, context, transcript-segments, prompt-started, calibration-complete, answer-complete, pause, telemetry, usage.
   - Deferred to Phase 2/5: evaluations, report, appeals, reviews.
7. **Errors and correlation:** RFC 9457 `application/problem+json` with a stable `code`; an `X-Request-ID` correlation ID on every response; body-size limits; a rate limit on invitation exchange.

## Consequences

Holding the LiveKit secret no longer means holding a session grant, and a passively observed nonce is worthless once claimed. A compromised agent worker host remains a trusted-infrastructure compromise; that residual risk is recorded in the threat-model addendum. SSO/OIDC for staff is a Phase 5/6 item.

## Verification

Integration tests cover cross-tenant and cross-candidate denial, Origin checks, invitation reuse and revocation, nonce reuse, stale fencing tokens, webhook signature/dedupe/staleness, an OpenAPI route-table check, and a "compromised agent" sequence suite.
