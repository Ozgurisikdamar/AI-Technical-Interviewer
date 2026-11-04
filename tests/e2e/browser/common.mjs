// Shared helpers for the browser E2E flows (run by tests/e2e/test_browser.py).
import { createHmac } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';

// The preinstalled Playwright (global node_modules, matching browser build); ESM imports ignore NODE_PATH.
const require = createRequire(import.meta.url);
export const { chromium } = require(path.join(process.env.NODE_PATH ?? '', 'playwright'));
// axe-core is pinned in ./package.json (npm ci in this directory).
const AXE_SOURCE = readFileSync(require.resolve('axe-core/axe.min.js'), 'utf8');

export const env = (name) => {
  const v = process.env[name];
  if (!v) throw new Error(`${name} is not set`);
  return v;
};
export const WEB = env('ATI_E2E_WEB_URL');
const CP = env('ATI_E2E_PUBLIC_URL');
const STAFF = env('ATI_E2E_STAFF_TOKEN');
const LK_API = env('ATI_E2E_LIVEKIT_API_URL');
const LK_KEY = env('ATI_E2E_LIVEKIT_API_KEY');
const LK_SECRET = env('ATI_E2E_LIVEKIT_API_SECRET');
export const FAKE_MIC = env('ATI_E2E_FAKE_MIC');

export const steps = [];
export const step = (name, ok, detail = '') => {
  steps.push({ name, ok, detail });
  if (!ok) throw new Error(`${name}${detail ? `: ${detail}` : ''}`);
};

const b64u = (o) => Buffer.from(JSON.stringify(o)).toString('base64url');
async function lk(method, body, grant) {
  const now = Math.floor(Date.now() / 1000);
  const h = b64u({ alg: 'HS256', typ: 'JWT' });
  const b = b64u({ iss: LK_KEY, sub: 'e2e-observer', nbf: now - 5, exp: now + 300, video: grant });
  const jwt = `${h}.${b}.${createHmac('sha256', LK_SECRET).update(`${h}.${b}`).digest('base64url')}`;
  const r = await fetch(`${LK_API}/twirp/livekit.RoomService/${method}`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${jwt}`, 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  return r.json();
}

/** The newest room in which a participant named "candidate" is connected. */
export async function findCandidateRoom() {
  const rooms = ((await lk('ListRooms', {}, { roomList: true })).rooms ?? []).sort(
    (a, b) => Number(b.creation_time ?? 0) - Number(a.creation_time ?? 0),
  );
  for (const r of rooms) {
    const parts = (await lk('ListParticipants', { room: r.name }, { roomAdmin: true, room: r.name })).participants ?? [];
    if (parts.some((p) => p.identity === 'candidate')) return r.name;
  }
  return null;
}

/** The candidate's audio tracks in a room, or null if the candidate is not in it. */
export async function candidateTracks(room) {
  const j = await lk('ListParticipants', { room }, { roomAdmin: true, room });
  const c = (j.participants ?? []).find((p) => p.identity === 'candidate');
  return c ? (c.tracks ?? []).filter((t) => t.type === 'AUDIO' || t.type === 1) : null;
}

export const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

export async function until(pred, timeout, label) {
  const t0 = Date.now();
  while (Date.now() - t0 < timeout) {
    try {
      if (await pred()) return true;
    } catch {
      /* transient */
    }
    await sleep(250);
  }
  throw new Error(`timed out: ${label}`);
}

/** Creates an interview as staff and returns the invitation token (fragment of the invitation URL). */
export async function createInvitation() {
  const created = await fetch(`${CP}/api/v1/interviews`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${STAFF}`, 'Content-Type': 'application/json', 'Idempotency-Key': `e2e-web-${Date.now()}-${Math.random()}` },
    body: JSON.stringify({ candidate_ref: `ref_web_${Date.now()}`, plan_id: 'plan_demo_backend_en', plan_version: 1 }),
  });
  if (created.status !== 201) throw new Error(`create interview: ${created.status}`);
  return (await created.json()).invitation_url.split('#token=')[1];
}

/**
 * Chromium with a fake microphone that loops the pitch-coded WAV; records policy violations.
 * microphone: 'granted' (default) or 'denied' (the permission request is refused).
 * device: false runs without any (fake) capture device.
 */
export async function launch({ microphone = 'granted', device = true } = {}) {
  const granted = microphone === 'granted';
  const browser = await chromium.launch({
    args: [
      ...(granted ? ['--use-fake-ui-for-media-stream'] : []),
      ...(device ? ['--use-fake-device-for-media-stream', `--use-file-for-fake-audio-capture=${FAKE_MIC}`] : []),
      '--autoplay-policy=no-user-gesture-required',
      '--no-proxy-server',
    ],
  });
  const context = await browser.newContext({ permissions: granted ? ['microphone'] : [] });
  const page = await context.newPage();
  const seen = { violations: [], internalCalls: [], postsWithoutKey: [], pageErrors: [], a11y: [], audited: [] };
  current = seen;
  watch(page, seen);
  return { browser, context, page, seen };
}

/** Records the policy violations of a page; launch() watches its own page, flows call it for others. */
export function watch(page, seen) {
  page.on('console', (m) => {
    if (/Content Security Policy|Refused to/i.test(m.text())) seen.violations.push(m.text());
  });
  page.on('pageerror', (e) => seen.pageErrors.push(String(e)));
  page.on('request', (r) => {
    const u = new URL(r.url());
    if (u.pathname.startsWith('/internal')) seen.internalCalls.push(u.pathname);
    if (r.method() === 'POST' && u.pathname.startsWith('/api/') && !r.headers()['idempotency-key']) seen.postsWithoutKey.push(u.pathname);
  });
}

let current = null;

/**
 * WCAG 2.1 A/AA audit of the current screen with axe-core (BP18 accessibility). axe is evaluated
 * through the DevTools protocol, so the page's CSP stays in force (no inline script is added).
 * Serious and critical violations fail the flow at the end; the others are reported.
 */
export async function audit(page, label) {
  // A string is evaluated by DevTools as an expression: no eval() in the page, no CSP exception.
  if (!(await page.evaluate(() => Boolean(window.axe)))) await page.evaluate(AXE_SOURCE);
  // Colours are measured at rest: a button that just became enabled is still in its 120 ms
  // background transition, and a mid-transition sample is not the colour a candidate reads.
  await page.evaluate(() =>
    Promise.all(
      document
        .getAnimations()
        .filter((a) => Number.isFinite(a.effect?.getComputedTiming().endTime ?? Infinity))
        .map((a) => a.finished.catch(() => undefined)),
    ),
  );
  const results = await page.evaluate(async () =>
    window.axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'] }, resultTypes: ['violations'] }),
  );
  current.audited.push(label);
  for (const v of results.violations) {
    current.a11y.push({ screen: label, id: v.id, impact: v.impact, nodes: v.nodes.slice(0, 3).map((n) => n.target.join(' ')) });
  }
}

/**
 * The document response must carry the security headers (ADR-016, THREAT_MODEL addendum).
 * microphone: 'self' for candidate pages, 'none' for the review console (ADR-023).
 */
export function checkDocumentHeaders(response, label, { microphone = 'self' } = {}) {
  const h = response.headers();
  const csp = h['content-security-policy'] ?? '';
  step(`${label}: CSP with a per-request nonce`, /script-src 'self' 'nonce-[A-Za-z0-9+/_-]{16,}={0,2}' 'strict-dynamic'/.test(csp), csp);
  for (const directive of ["default-src 'self'", "object-src 'none'", "base-uri 'none'", "frame-ancestors 'none'", "frame-src 'none'"]) {
    step(`${label}: CSP ${directive}`, csp.includes(directive), csp);
  }
  step(`${label}: no unsafe-inline or unsafe-eval scripts`, !/script-src[^;]*'unsafe-(inline|eval)'/.test(csp), csp);
  step(`${label}: Referrer-Policy no-referrer`, h['referrer-policy'] === 'no-referrer', h['referrer-policy']);
  step(`${label}: X-Frame-Options DENY`, h['x-frame-options'] === 'DENY', h['x-frame-options']);
  step(`${label}: nosniff`, h['x-content-type-options'] === 'nosniff', h['x-content-type-options']);
  const policy = h['permissions-policy'] ?? '';
  if (microphone === 'self') {
    step(`${label}: microphone only for this origin`, policy.includes('microphone=(self)') && policy.includes('camera=()'), policy);
  } else {
    step(`${label}: no microphone, camera or location`, policy.includes('microphone=()') && policy.includes('camera=()') && policy.includes('geolocation=()'), policy);
  }
}

/** Exchanges the invitation (user click) and answers every purpose of the notice. */
export async function consent(page, token, { voice = true, transcript = true, ai = true, recording = false } = {}) {
  const response = await page.goto(`${WEB}/i#token=${token}`);
  await page.getByTestId('continue').waitFor();
  await audit(page, 'invitation');
  await page.getByTestId('continue').click();
  await page.locator('#consent-heading').waitFor();
  await audit(page, 'consent');
  const id = new URL(page.url()).pathname.split('/').filter(Boolean).pop() ?? '';
  for (const [purpose, yes] of [
    ['voice_interview_processing', voice],
    ['transcript_assessment_record', transcript],
    ['ai_assisted_evaluation_with_human_review', ai],
    ['audio_recording', recording],
  ]) {
    await page.getByTestId(`consent-${purpose}-${yes ? 'yes' : 'no'}`).check();
  }
  await page.getByTestId('consent-submit').click();
  return { id, response };
}

/** Withdraws from the room (confirmation dialog) and waits for the cancelled screen. */
export async function withdraw(page) {
  await page.getByTestId('withdraw').click();
  await page.getByTestId('withdraw-confirm').click();
  await page.locator('#cancelled-heading').waitFor({ timeout: 15_000 });
}

/** Common policy checks at the end of every flow. */
export function checkPolicies(seen) {
  step('no CSP violations', seen.violations.length === 0, seen.violations.slice(0, 2).join(' | '));
  step('no request to internal endpoints', seen.internalCalls.length === 0, seen.internalCalls.join(','));
  step('every API POST carried an Idempotency-Key', seen.postsWithoutKey.length === 0, seen.postsWithoutKey.join(','));
  step('no uncaught page errors', seen.pageErrors.length === 0, seen.pageErrors.slice(0, 2).join(' | '));
  const blocking = seen.a11y.filter((v) => v.impact === 'serious' || v.impact === 'critical');
  steps.push({ name: `accessibility audits (${seen.audited.join(', ')})`, ok: blocking.length === 0, detail: JSON.stringify(seen.a11y) });
  step('no serious or critical accessibility violations', blocking.length === 0, JSON.stringify(blocking));
}

/** Credentials a flow may handle; they are masked in everything it prints (the output reaches test logs). */
const SECRET_ENV = ['ATI_E2E_STAFF_TOKEN', 'ATI_E2E_REVIEWER_TOKEN', 'ATI_E2E_LIVEKIT_API_SECRET'];
const extraSecrets = [];

/** Masks a value the flow learned at run time (a session cookie) in its output. */
export function secret(value) {
  if (value && value.length >= 16) extraSecrets.push(value);
  return value;
}

function redact(text) {
  let out = text;
  for (const value of [...SECRET_ENV.map((name) => process.env[name]), ...extraSecrets]) {
    if (value && value.length >= 16) out = out.split(value).join('[redacted]');
  }
  return out;
}

/** Runs a flow, prints one JSON result line and exits with its status. */
export async function run(flow, options = {}) {
  let browser;
  const result = { interview_id: '' };
  try {
    const launched = await launch(options);
    browser = launched.browser;
    await flow({ ...launched, result });
    checkPolicies(launched.seen);
    console.log(redact(JSON.stringify({ ok: true, ...result, steps, a11y: launched.seen.a11y })));
    await browser.close();
    process.exit(0);
  } catch (e) {
    console.log(redact(JSON.stringify({ ok: false, ...result, error: String(e), steps })));
    if (browser) await browser.close();
    process.exit(1);
  }
}
