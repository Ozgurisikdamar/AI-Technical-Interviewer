// Review console (ADR-023): a reviewer signs in with a staff token, decides every answer of a
// completed interview independently of the AI draft, reads the report and signs out; a recruiter
// then reads the report but cannot review. The interview was prepared by test_browser.py.
import { appendFileSync, mkdirSync } from 'node:fs';
import path from 'node:path';
import { WEB, audit, checkDocumentHeaders, env, run, secret, step, until, watch } from './common.mjs';

const REVIEWER = env('ATI_E2E_REVIEWER_TOKEN');
const RECRUITER = env('ATI_E2E_STAFF_TOKEN');
const INTERVIEW = env('ATI_E2E_INTERVIEW_ID');
const SECRETS = env('ATI_E2E_SECRETS_FILE');
const SHOTS = process.env.ATI_E2E_SCREENSHOTS ?? '';
const SCORES = { technical_accuracy: '60', architecture_maturity: '50', edge_case_awareness: '40', claim_support: '30' };
const RATIONALE = 'Synthetic review recorded by the browser end-to-end test.';
/** Reasons that come from the AI evaluation: never shown before the draft is opened. */
const AI_REASONS = ['low_confidence', 'evaluator_requested_review', 'evaluator_output_risk', 'transcript_quality'];

async function shot(page, name) {
  if (!SHOTS) return;
  mkdirSync(SHOTS, { recursive: true });
  await page.screenshot({ path: path.join(SHOTS, `${name}.png`), fullPage: true });
}

const activeTestId = (page) => page.evaluate(() => document.activeElement?.getAttribute('data-testid') ?? document.activeElement?.id ?? '');

function consoleHeaders(response, label) {
  const h = response.headers();
  checkDocumentHeaders(response, label, { microphone: 'none' });
  step(`${label}: noindex`, (h['x-robots-tag'] ?? '').includes('noindex'), h['x-robots-tag']);
  step(`${label}: not stored by caches`, /no-store/.test(h['cache-control'] ?? ''), h['cache-control']);
}

async function sessionCookie(context) {
  return (await context.cookies()).find((c) => c.name === 'ati_ss') ?? null;
}

/** The token must be nowhere the page can read or leak it: address, markup, inputs, storage or script-visible cookies. */
async function tokenIsGone(page, token, label) {
  const exposed = await page.evaluate(
    (t) => [
      location.href.includes(t),
      document.documentElement.outerHTML.includes(t),
      [...document.querySelectorAll('input, textarea')].some((i) => i.value.includes(t)),
      JSON.stringify(Object.entries(localStorage)).includes(t) || JSON.stringify(Object.entries(sessionStorage)).includes(t),
      document.cookie.includes(t) || document.cookie.includes('ati_ss'),
    ],
    token,
  );
  step(`${label}: the token is not in the address, page, inputs, storage or document.cookie`, !exposed.some(Boolean), JSON.stringify(exposed));
}

async function signInAs(page, token, label) {
  await until(() => page.getByTestId('staff-token').isEnabled(), 15_000, 'sign-in form hydrated');
  await page.getByTestId('staff-token').fill(token);
  const [response] = await Promise.all([
    page.waitForResponse((r) => new URL(r.url()).pathname === '/api/v1/staff/session' && r.request().method() === 'POST'),
    page.getByTestId('staff-sign-in').click(),
  ]);
  step(`${label} signs in`, response.status() === 200, String(response.status()));
}

async function scoreInputsEmpty(page) {
  const values = await Promise.all([...Object.keys(SCORES).map((d) => page.getByTestId(`score-${d}`).inputValue()), page.getByTestId('decision-rationale').inputValue()]);
  return values.every((v) => v === '');
}

await run(
  async ({ browser, context, page, seen, result }) => {
    result.interview_id = INTERVIEW;

    // 1. The sign-in screen.
    const signInDoc = await page.goto(`${WEB}/staff`);
    consoleHeaders(signInDoc, 'sign-in');
    await until(() => page.getByTestId('staff-token').isEnabled(), 15_000, 'sign-in form hydrated');
    const field = page.getByTestId('staff-token');
    step('the token field has no name, so no form submission can carry it', (await field.getAttribute('name')) === null);
    step('the token field masks the token', (await field.getAttribute('type')) === 'password' && (await field.getAttribute('autocomplete')) === 'off');
    await audit(page, 'sign-in');
    let signIns = 0;
    page.on('request', (r) => {
      if (r.method() === 'POST' && new URL(r.url()).pathname === '/api/v1/staff/session') signIns += 1;
    });
    await field.fill('stf_this-is-an-id-not-a-token');
    await page.getByTestId('staff-sign-in').click();
    await page.getByTestId('staff-sign-in-problem').waitFor();
    step('a malformed token is caught in the browser, without a request', signIns === 0);
    step('the problem takes focus and the field is marked invalid', (await activeTestId(page)) === 'staff-sign-in-problem' && (await field.getAttribute('aria-invalid')) === 'true');
    await audit(page, 'sign-in problem');
    await field.fill(`ati_st_${'u'.repeat(43)}`);
    const [refused] = await Promise.all([
      page.waitForResponse((r) => new URL(r.url()).pathname === '/api/v1/staff/session' && r.request().method() === 'POST'),
      page.getByTestId('staff-sign-in').click(),
    ]);
    await page.getByTestId('staff-sign-in-problem').waitFor();
    step('an unknown token is refused', refused.status() === 401, String(refused.status()));
    step('a refused sign-in sets no session cookie', (await sessionCookie(context)) === null);

    // 2. The reviewer signs in: an HttpOnly cookie, and the token is gone.
    await signInAs(page, REVIEWER, 'the reviewer');
    await page.waitForURL(`${WEB}/staff/review`);
    const cookie = await sessionCookie(context);
    step('the session cookie is HttpOnly, SameSite=Strict and scoped to /api', cookie !== null && cookie.httpOnly && cookie.sameSite === 'Strict' && cookie.path === '/api', JSON.stringify({ ...cookie, value: '…' }));
    appendFileSync(SECRETS, `${secret(cookie.value)}\n`, { mode: 0o600 });
    await tokenIsGone(page, REVIEWER, 'after sign-in');
    await page.getByTestId('staff-who').waitFor();
    step('the console shows who is signed in', (await page.getByTestId('staff-who').innerText()).includes('(reviewer)'));

    // 3. The queue of this interview.
    const queueDoc = await page.goto(`${WEB}/staff/review?interview=${INTERVIEW}`);
    consoleHeaders(queueDoc, 'queue');
    await page.getByTestId('queue-task').first().waitFor();
    step('the queue holds the four answers of the interview', (await page.getByTestId('queue-task').count()) === 4);
    step('the queue shows process reasons only', (await page.locator(AI_REASONS.map((r) => `[data-reason="${r}"]`).join(', ')).count()) === 0);
    step('every answer is reviewed by a person', (await page.locator('[data-reason="policy_all_reviewed"]').count()) === 4);
    await audit(page, 'queue');
    await shot(page, '1-queue');
    await page.getByTestId('queue-open-task').first().click();

    // 4. Each answer, independently of the AI draft. The third is done with the keyboard only.
    for (let i = 1; i <= 4; i += 1) {
      const label = `answer ${i}`;
      await page.getByTestId('decision-form').waitFor();
      step(`${label}: the heading has focus`, (await activeTestId(page)) === 'task-heading');
      step(`${label}: the form starts empty`, await scoreInputsEmpty(page));
      const toggle = page.getByTestId('ai-draft-toggle');
      step(`${label}: the AI draft is closed and not rendered`, (await toggle.getAttribute('aria-expanded')) === 'false' && (await page.getByTestId('ai-draft-panel').count()) === 0);
      step(`${label}: no AI-derived reason before the draft`, (await page.locator(AI_REASONS.map((r) => `[data-reason="${r}"]`).join(', ')).count()) === 0);
      step(`${label}: passages the draft cites are highlighted`, (await page.getByTestId('cited').count()) > 0);
      step(`${label}: the transcript and question carry their language`, (await page.getByTestId('transcript').getAttribute('lang')) === 'en' && (await page.getByTestId('task-question').getAttribute('lang')) === 'en');
      await audit(page, `${label}`);
      if (i === 1) await shot(page, '2-task-draft-closed');

      if (i === 3) {
        // Keyboard only: Tab to the first score, type, Tab through the form, Enter.
        for (let n = 0; n < 60 && (await activeTestId(page)) !== 'score-technical_accuracy'; n += 1) await page.keyboard.press('Tab');
        step(`${label}: Tab reaches the first score`, (await activeTestId(page)) === 'score-technical_accuracy');
        for (const [d, v] of Object.entries(SCORES)) {
          step(`${label}: Tab order reaches ${d}`, (await activeTestId(page)) === `score-${d}`);
          await page.keyboard.type(v);
          await page.keyboard.press('Tab');
        }
        step(`${label}: Tab order reaches the rationale`, (await activeTestId(page)) === 'decision-rationale');
        await page.keyboard.type(RATIONALE);
        await page.keyboard.press('Tab');
        step(`${label}: Tab order reaches the submit button`, (await activeTestId(page)) === 'decision-submit');
        step(`${label}: the weighted preview`, (await page.getByTestId('decision-preview').innerText()).includes('49.00'));
        await page.keyboard.press('Enter');
      } else {
        await page.getByTestId('decision-submit').click();
        await page.getByTestId('decision-errors').waitFor();
        step(`${label}: an empty decision lists its problems and takes focus`, (await activeTestId(page)) === 'decision-errors');
        step(`${label}: the fields are marked invalid`, (await page.getByTestId('score-claim_support').getAttribute('aria-invalid')) === 'true' && (await page.getByTestId('decision-rationale').getAttribute('aria-invalid')) === 'true');
        await audit(page, `${label} problems`);

        await toggle.click();
        await page.getByTestId('ai-draft-panel').waitFor();
        step(`${label}: focus stays on the draft button`, (await activeTestId(page)) === 'ai-draft-toggle' && (await toggle.getAttribute('aria-expanded')) === 'true');
        step(`${label}: the draft shows the AI scores`, (await page.getByTestId('ai-draft-scores').locator('td').count()) === 4);
        step(`${label}: the draft never prefills the form`, await scoreInputsEmpty(page));
        step(`${label}: AI-derived reasons appear with the draft`, (await page.getByTestId('ai-draft-reasons').locator('[data-reason="low_confidence"]').count()) === 1);
        await audit(page, `${label} draft`);
        if (i === 1) await shot(page, '3-task-draft-open');

        for (const [d, v] of Object.entries(SCORES)) await page.getByTestId(`score-${d}`).fill(v);
        await page.getByTestId('decision-rationale').fill(RATIONALE);
        step(`${label}: the weighted preview`, (await page.getByTestId('decision-preview').innerText()).includes('49.00'));
        await page.getByTestId('decision-submit').click();
      }

      await page.getByTestId('recorded').waitFor();
      step(`${label}: the confirmation takes focus`, (await activeTestId(page)) === 'task-recorded');
      if (i < 4) {
        if (i === 3) {
          await page.keyboard.press('Tab');
          step(`${label}: Tab reaches the next answer`, (await activeTestId(page)) === 'recorded-next');
          await page.keyboard.press('Enter');
        } else {
          await page.getByTestId('recorded-next').click();
        }
        await until(async () => (await page.getByTestId('recorded').count()) === 0, 10_000, `${label}: next answer`);
      } else {
        await audit(page, `${label} recorded`);
        await page.getByTestId('recorded-report').click();
      }
    }

    // 5. The report.
    await page.getByTestId('report-answer').first().waitFor();
    step('the report has the four answers', (await page.getByTestId('report-answer').count()) === 4);
    step('the report says it is decision support only', (await page.getByTestId('report-notice').innerText()).includes('Decision support only'));
    const weighted = await page.getByTestId('report-weighted').allInnerTexts();
    step("the report shows the reviewer's weighted scores", weighted.length === 4 && weighted.every((w) => w.includes('49.00')), JSON.stringify(weighted));
    step('the AI drafts are folded', await page.evaluate(() => [...document.querySelectorAll('details')].every((d) => !d.open)));
    await audit(page, 'report');
    await shot(page, '4-report');

    // 6. Sign out: the cookie is cleared, the console asks for sign-in, and the old cookie is dead.
    await page.getByTestId('staff-sign-out').click();
    await page.waitForURL(`${WEB}/staff`);
    await page.getByTestId('staff-signed-out').waitFor();
    step('signing out clears the session cookie', (await sessionCookie(context)) === null);
    await page.goto(`${WEB}/staff/review`);
    await page.getByTestId('staff-sign-in-required').waitFor();
    step('after signing out, the queue asks to sign in', true);
    await audit(page, 'signed out');
    const replay = await fetch(`${WEB}/api/v1/review-tasks`, { headers: { Cookie: `ati_ss=${cookie.value}` } });
    step('the old session cookie is refused by the API', replay.status === 401, String(replay.status));

    // 7. A recruiter reads reports and cannot review.
    const recruiterContext = await browser.newContext();
    const rp = await recruiterContext.newPage();
    watch(rp, seen);
    const reportsDoc = await rp.goto(`${WEB}/staff`);
    consoleHeaders(reportsDoc, 'recruiter sign-in');
    await signInAs(rp, RECRUITER, 'the recruiter');
    await rp.waitForURL(`${WEB}/staff/reports`);
    const recruiterCookie = await sessionCookie(recruiterContext);
    step('the recruiter has a session cookie', recruiterCookie !== null && recruiterCookie.httpOnly);
    appendFileSync(SECRETS, `${secret(recruiterCookie.value)}\n`, { mode: 0o600 });
    await tokenIsGone(rp, RECRUITER, 'recruiter');
    await rp.locator(`[data-interview="${INTERVIEW}"]`).waitFor();
    step('the recruiter has no review navigation', (await rp.getByTestId('staff-nav-review').count()) === 0 && (await rp.getByTestId('staff-nav-reports').count()) === 1);
    step('the report list holds no scores', !(await rp.getByTestId('report-list').innerText()).includes('49.00'));
    await audit(rp, 'reports');
    await rp.locator(`[data-interview="${INTERVIEW}"]`).getByTestId('report-open').click();
    await rp.getByTestId('report-answer').first().waitFor();
    step('the recruiter reads the report', (await rp.getByTestId('report-answer').count()) === 4);
    await rp.goto(`${WEB}/staff/review`);
    await rp.getByTestId('staff-not-permitted').waitFor();
    step('the review queue is not available to a recruiter', true);
    await audit(rp, 'not permitted');
    await recruiterContext.close();
  },
  { microphone: 'denied', device: false },
);
