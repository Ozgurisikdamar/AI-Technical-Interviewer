# Control plane

The Go service that owns interview state (ADR-002, ADR-012, ADR-016): the backend-enforced state machine, consent,
invitations, LiveKit rooms and agent dispatch, the Tier-1 agent's claim and lease, transcript ingestion and
attribution, the completion protocol, the transactional outbox, optional recording, staff invitation revocation, and
audit. From Phase 2 it also validates Tier-2 evaluations and serves human review and reports (ADR-022), including the
review console's staff sessions (ADR-023), and candidate sign-in with an application number and an access code
(ADR-024). Credentials (invitation, access code, session cookies, media token, agent grant) are returned once and never
kept at rest, not even for idempotent replays.

## Layout

| Package | Role |
|---|---|
| `internal/flow` | Pure state-machine engine loaded from the embedded contract (no I/O; architecture-tested) |
| `internal/interview` | Transactional transition service: guards, effects, ingestion, completion, leases, dispatch, views, text responses |
| `internal/tier1ctx` | Tier-1 context builder over allowlisted views, connected as `ati_tier1_ctx` (validated against tier1-context/v1) |
| `internal/recording` | Fail-closed recording policy, egress lifecycle, purge (ADR-017) |
| `internal/realtime` | LiveKit server API (rooms, tokens, dispatch, webhooks, egress) behind interfaces, plus test fakes |
| `internal/api` | Public listener (`/api/v1`, same-origin) and internal listener (`/internal/v1`, webhooks, health, metrics) |
| `internal/store` | Migrations (goose), least-privilege roles, plan seeding |
| `internal/accesscode` | Application numbers and access codes: generation, display, forgiving input, argon2id (ADR-024) |
| `internal/config`, `internal/logging`, `internal/httpx`, `internal/ids` | Configuration with ADR-020 refusals, redacting logs, HTTP plumbing, identifiers |
| `cmd/control-plane`, `cmd/ati-admin` | The server and the administration CLI (roles, migrations, tenants, staff tokens and their revocation, recording policy and purge, operational purge, offline evaluation validation) |

Staff authenticate with a bearer token (`ati-admin create-staff-token`), or in the review console with the session
cookie it is exchanged for (`/api/v1/staff/session`). The cookie counts only for review, reports and the session
itself; interview management keeps requiring the bearer token. `ati-admin list-staff-tokens` shows ids, roles and
expiry without secrets, and `ati-admin revoke-staff-token --token-id` ends a token and its browser sessions at once.

Candidates enter through the invitation link or by signing in (`/api/v1/candidate/session`) with the application
number and the access code returned when the interview was created. Both open the same session cookie. Every refusal
is the same 401; five wrong codes in a row lock code sign-in for 15 minutes (the link keeps working); sign-ins are
rate-limited per client (`ATI_CANDIDATE_SIGNINS_PER_MINUTE`). Reviewers never see the application number.

## Run and test

```bash
make go-lint go-test       # from the repository root; starts an ephemeral PostgreSQL 16 if needed
tools/dev/stack.sh up      # the full local stack, including this service
```

Configuration is environment-only; see `.env.example` at the repository root. The server refuses to start without
`APP_ENV`, refuses production unless the production gates, approved notice, TLS URLs, retention and a least-privilege
database role are in place (ADR-020), and always marks non-production responses.
