# Web UI: candidate pages and review console

Next.js 16 / React 19 / TypeScript 6 with `livekit-client`. The browser talks only to same-origin `/api/v1/...`
(rewritten to the control plane's public listener) and to LiveKit for media.

Two route groups share the document shell (`src/app/layout.tsx`) and nothing else:

| Group | Paths | Audience |
|---|---|---|
| `src/app/(candidate)` | `/` (sign-in), `/i` (invitation link), `/interview/{id}` | Candidates (a route group adds no path segment) |
| `src/app/staff` | `/staff`, `/staff/review`, `/staff/review/{task}`, `/staff/reports`, `/staff/reports/{interview}` | Staff: the review console (ADR-023) |

## Candidate pages

- **Sign-in** (`/`, ADR-024): the application number and the access code the hiring team sent.
  - Typing is forgiving: letter case, spaces and hyphens are ignored, and O reads as 0 and I or L as 1, exactly as the
    control plane reads them (`src/lib/signin/codes.ts`, tested with the shared cases in
    `contracts/fixtures/sign-in-codes`). Input the server would refuse for its format is refused without a request.
  - Neither field has a `name`; both stay disabled until the page has hydrated. The code is a password field that can
    be shown, accepts pasting, and is cleared once the server accepts it. Nothing is stored in the browser.
  - Every refusal reads the same. After five wrong codes in a row, code sign-in pauses for 15 minutes; the invitation
    link keeps working.
  - The notice and setup screens then show the matched application (the interview's title, in its own language, and
    the application number) and the steps: notice and choices, setup, interview.
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
  - The control plane exchanges it for a session cookie: HttpOnly, SameSite=Strict, `/api` only, 8 hours by default
    and never longer than the token.
  - The page never stores the token. The field has no `name`, stays disabled until the page has hydrated, and is
    cleared after use.
  - Use short-lived tokens for the console (`--ttl`).
  - Afterwards the console returns to the page that asked for a sign-in, but only if it is a console page the role
    may open (`safeNext` in `src/lib/staff/nav.ts`); otherwise it opens the role's home.
- **Queue:** the answers waiting for review, grouped by interview, with the reasons that concern the process.
- **Answer:**
  - the question, the candidate's transcript and the rubric;
  - the passages the AI draft cites are underlined neutrally, using the control plane validator's quote rule
    (`src/lib/staff/highlight.ts`, tested with the validator's cases in `contracts/fixtures/quote-matching`);
  - the reviewer scores each dimension and writes a rationale;
  - the AI draft stays closed, and unrendered, until the reviewer opens it. It never prefills the form, and the
    decision records whether it was opened;
  - if the session ends meanwhile, the form keeps what was typed and offers sign-in in a new tab.
- **Reports:** the list holds no scores and no candidate details; a report shows the reviewers' decisions, with the AI
  parts folded.
- Lists show the API's largest page (200) and say so when they may have been cut off.
- Recruiters read reports; reviewers and administrators also review. The server enforces the roles. The console only
  hides what a role cannot open: `staff/review/layout.tsx` and `staff/reports/layout.tsx` require a session whose role
  may open the section.

## Languages

Turkish is the default; English can be chosen from the switch in every page's header.

- Strings live only in the catalogs `src/i18n/tr.ts` and `src/i18n/en.ts`. `tr.ts` is typed as `Messages`, and
  `catalogs.test.ts` checks that both hold the same keys, that no text is empty and that every message is translated.
  Both texts still need review: the Turkish by a native speaker, and the notices by legal counsel.
- The choice is kept in the `ati_lang` cookie (`tr` or `en`, one year), written only when someone picks a language
  (`src/i18n/preference.ts`, the only code allowed to touch `document.cookie`). The server reads it for every request
  (`src/i18n/server.ts`); `Accept-Language` is not consulted.
- Switching re-renders the page from the server (`router.refresh()`) without a reload, so an invitation not yet
  exchanged, choices, typed answers and unsaved scores survive it.
- Dates (`4 Kas 2026 13:13 UTC`, `4 Nov 2026, 13:13 UTC`), decimals (`49,00`, `49.00`) and counts follow the
  language. The interview's own text (questions, captions, the title) carries the interview's language.

## Design

Navy and gold, defined as tokens in `src/app/globals.css`; components keep to CSS modules.

- Every colour pair meets WCAG 2.2 AA (text at least 4.5:1, borders and focus rings at least 3:1); the browser flows
  audit every screen with axe-core, `wcag22aa` included.
- Gold is an accent only: the rule under the header, small eyebrow labels on white, the focus ring and the chosen
  language. The light gold (`--color-gold-rule`) never carries text on a light surface.
- Text always sits on a solid colour. No `style` attributes and no `data:` URIs (the CSP refuses both), and no external
  fonts. The header is not sticky (WCAG 2.2, SC 2.4.11), and its chrome holds no `<details>` and no links beyond the
  console's navigation. Controls are at least 44 pixels high.

## Security

- Per-request CSP nonce and strict security headers. Console pages also send `X-Robots-Tag: noindex` and a
  Permissions-Policy without the microphone.
- Lint rules forbid internal paths, data-channel publishing, browser storage, `document.cookie` outside the language
  preference, and `NEXT_PUBLIC_` values.
- Responses are validated before use (`src/lib/api/parse.ts`, `staffParse.ts`). Query parameters come from an
  allow-list (`buildQuery`).
- A bundle scan looks for secrets, including staff tokens.

```bash
make web-test   # from the repository root: eslint, tsc, unit tests, production build, bundle scan
```

`ATI_CONTROL_PLANE_URL` is read at build time (rewrites); `ATI_LIVEKIT_PUBLIC_URL` and `APP_ENV` at request time. See
`.env.example`. Browser end-to-end flows run against the real stack in `tests/e2e/browser`; `signin_flow.mjs` covers
candidate sign-in in Turkish, and `reviewer_flow.mjs` the review console.
