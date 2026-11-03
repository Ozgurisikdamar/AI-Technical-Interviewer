// Shared helpers for the browser E2E flows (run by tests/e2e/test_browser.py).
import { createHmac } from 'node:crypto';
import { createRequire } from 'node:module';
import path from 'node:path';

// The preinstalled Playwright (global node_modules, matching browser build); ESM imports ignore NODE_PATH.
const require = createRequire(import.meta.url);
export const { chromium } = require(path.join(process.env.NODE_PATH ?? '', 'playwright'));

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
 */
export async function launch({ microphone = 'granted' } = {}) {
  const granted = microphone === 'granted';
  const browser = await chromium.launch({
    args: [
      ...(granted ? ['--use-fake-ui-for-media-stream'] : []),
      '--use-fake-device-for-media-stream',
      `--use-file-for-fake-audio-capture=${FAKE_MIC}`,
      '--autoplay-policy=no-user-gesture-required',
      '--no-proxy-server',
    ],
  });
  const context = await browser.newContext({ permissions: granted ? ['microphone'] : [] });
  const page = await context.newPage();
  const seen = { violations: [], internalCalls: [], postsWithoutKey: [], pageErrors: [] };
  page.on('console', (m) => {
    if (/Content Security Policy|Refused to/i.test(m.text())) seen.violations.push(m.text());
  });
  page.on('pageerror', (e) => seen.pageErrors.push(String(e)));
  page.on('request', (r) => {
    const u = new URL(r.url());
    if (u.pathname.startsWith('/internal')) seen.internalCalls.push(u.pathname);
    if (r.method() === 'POST' && u.pathname.startsWith('/api/') && !r.headers()['idempotency-key']) seen.postsWithoutKey.push(u.pathname);
  });
  return { browser, context, page, seen };
}

/** Common policy checks at the end of every flow. */
export function checkPolicies(seen) {
  step('no CSP violations', seen.violations.length === 0, seen.violations.slice(0, 2).join(' | '));
  step('no request to internal endpoints', seen.internalCalls.length === 0, seen.internalCalls.join(','));
  step('every API POST carried an Idempotency-Key', seen.postsWithoutKey.length === 0, seen.postsWithoutKey.join(','));
  step('no uncaught page errors', seen.pageErrors.length === 0, seen.pageErrors.slice(0, 2).join(' | '));
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
    console.log(JSON.stringify({ ok: true, ...result, steps }));
    await browser.close();
    process.exit(0);
  } catch (e) {
    console.log(JSON.stringify({ ok: false, ...result, error: String(e), steps }));
    if (browser) await browser.close();
    process.exit(1);
  }
}
