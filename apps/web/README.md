# Candidate web UI

Next.js 16 / React 19 / TypeScript 6 with `livekit-client`. The browser talks only to same-origin `/api/v1/...`
(rewritten to the control plane's public listener) and to LiveKit for media.

- **Invitation:** the token lives only in the URL fragment, is removed from the address bar at once and is exchanged
  only after the candidate clicks Continue.
- **Consent:** one decision per purpose of the pinned notice (currently a DRAFT); recording is optional.
- **Device check:** level meter; text mode is offered when the microphone is unavailable or voice was declined. A
  blocked microphone is explained as blocked (with the fix) even when the browser raises another error, by checking the
  Permissions API.
- **Room:** publishes only the microphone and only while answers are being taken (unpublished while paused); captions
  of the interviewer; ARIA live status; controls enabled from the server's `allowed_actions`; refresh rejoins; losing
  the microphone pauses the interview.
- **Security:** per-request CSP nonce, strict security headers, lint rules that forbid internal paths, data-channel
  publishing, browser storage and `NEXT_PUBLIC_` values, and a bundle scan for secrets.

```bash
make web-test   # from the repository root: eslint, tsc, unit tests, production build, bundle scan
```

`ATI_CONTROL_PLANE_URL` is read at build time (rewrites); `ATI_LIVEKIT_PUBLIC_URL` and `APP_ENV` at request time. See
`.env.example`. Browser end-to-end flows run against the real stack in `tests/e2e/browser`.
