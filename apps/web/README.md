# Web UI: candidate pages and review console

Next.js 16 / React 19 / TypeScript 6 with `livekit-client`. The browser talks only to same-origin `/api/v1/...`
(rewritten to the control plane's public listener) and to LiveKit for media.

Two route groups share the document shell (`src/app/layout.tsx`) and nothing else:

| Group | Paths | Audience |
|---|---|---|
| `src/app/(candidate)` | `/`, `/i`, `/interview/{id}` | Candidates (a route group adds no path segment) |
| `src/app/staff` | `/staff`, `/staff/review`, `/staff/review/{task}`, `/staff/reports`, `/staff/reports/{interview}` | Staff: the review console (ADR-023) |

## Candidate pages

- **Invitation:** the token lives only in the URL fragment, is removed from the address bar at once and is exchanged
  only after the candidate clicks Continue.
- **Consent:** one decision per purpose of the pinned notice (currently a DRAFT); recording is optional.
- **Device check:** level meter; text mode is offered when the microphone is unavailable or voice was declined. A
  blocked microphone is explained as blocked (with the fix) even when the browser raises another error, by checking the
  Permissions API.
- **Room:** publishes only the microphone and only while answers are being taken (unpublished while paused); captions
  of the interviewer; ARIA live status; controls enabled from the server's `allowed_actions`; refresh rejoins; losing
  the microphone pauses the interview.

## Review console (`/staff`, ADR-023)

- **Sign-in:** a staff token from `ati-admin create-staff-token` is pasted once.
  - The control plane exchanges it for a session cookie: HttpOnly, SameSite=Strict, `/api` only, at most 8 hours.
  - The page never stores the token. The field has no `name`, stays disabled until the page has hydrated, and is
    cleared after use.
  - Use short-lived tokens for the console (`--ttl`).
- **Queue:** the answers waiting for review, grouped by interview, with the reasons that concern the process.
- **Answer:**
  - the question, the candidate's transcript and the rubric;
  - the passages the AI draft cites are underlined neutrally, using the control plane validator's quote rule
    (`src/lib/staff/highlight.ts`, tested with the validator's cases in `contracts/fixtures/quote-matching`);
  - the reviewer scores each dimension and writes a rationale;
  - the AI draft stays closed, and unrendered, until the reviewer opens it. It never prefills the form, and the
    decision records whether it was opened.
- **Reports:** the list holds no scores and no candidate details; a report shows the reviewers' decisions, with the AI
  parts folded.
- Recruiters read reports; reviewers and administrators also review. The server enforces the roles. The console only
  hides what a role cannot open.

## Security

- Per-request CSP nonce and strict security headers. Console pages also send `X-Robots-Tag: noindex` and a
  Permissions-Policy without the microphone.
- Lint rules forbid internal paths, data-channel publishing, browser storage and `NEXT_PUBLIC_` values.
- Responses are validated before use (`src/lib/api/parse.ts`, `staffParse.ts`). Query parameters come from an
  allow-list (`buildQuery`).
- A bundle scan looks for secrets, including staff tokens.

```bash
make web-test   # from the repository root: eslint, tsc, unit tests, production build, bundle scan
```

`ATI_CONTROL_PLANE_URL` is read at build time (rewrites); `ATI_LIVEKIT_PUBLIC_URL` and `APP_ENV` at request time. See
`.env.example`. Browser end-to-end flows run against the real stack in `tests/e2e/browser`; `reviewer_flow.mjs` covers
the review console.
