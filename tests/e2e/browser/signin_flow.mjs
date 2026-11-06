// Candidate sign-in (ADR-024) in Turkish, the default language, with no language chosen
// beforehand: the start page, a switch to English and back without a reload, a malformed number,
// a wrong code, the right code typed loosely, the matched application on the notice, the setup and
// the room, then withdrawal. test_browser.py created the interview, passes its application number
// and access code, and checks the database afterwards.
import { appendFileSync } from 'node:fs';
import { WEB, activeTestId, audit, checkDocumentHeaders, consentChoices, env, run, secret, shot, step, withdraw } from './common.mjs';

const NUMBER = env('ATI_E2E_APPLICATION_NUMBER');
const CODE = secret(env('ATI_E2E_ACCESS_CODE'));
const BARE = secret(CODE.replace('-', ''));
const SECRETS = env('ATI_E2E_SECRETS_FILE');
/** The right format, the wrong code: the last symbol changed. */
const WRONG = `${CODE.slice(0, -1)}${CODE.endsWith('A') ? 'B' : 'A'}`;
const SIGN_IN = '/api/v1/candidate/session';

const lang = (page) => page.locator('html').getAttribute('lang');
const invalid = async (locator) => (await locator.getAttribute('aria-invalid')) === 'true';

await run(
  async ({ page, context, result }) => {
    const requested = [];
    const signIns = [];
    page.on('request', (r) => {
      requested.push(r.url());
      if (new URL(r.url()).pathname === SIGN_IN) signIns.push(r.method());
    });

    // The start page: Turkish when no language was chosen.
    const home = await page.goto(`${WEB}/`);
    checkDocumentHeaders(home, 'start page');
    step('the start page is in Turkish by default', (await lang(page)) === 'tr');
    step('Turkish is the chosen language', (await page.getByTestId('lang-tr').getAttribute('aria-pressed')) === 'true');
    step('no language cookie before a choice', !(await context.cookies()).some((c) => c.name === 'ati_lang'));
    const number = page.getByTestId('sign-in-number');
    const code = page.getByTestId('sign-in-code');
    const submit = page.getByTestId('sign-in-submit');
    await number.fill(NUMBER.toLowerCase()); // fill() waits until the page has hydrated
    const fields = await page.evaluate(() =>
      [...document.querySelectorAll('form input')].map((i) => ({ name: i.name, type: i.type, autocomplete: i.autocomplete })),
    );
    step(
      'the fields have no name, are not remembered, and the code is hidden',
      fields.length === 2 && fields.every((f) => f.name === '' && f.autocomplete === 'off') && fields[1].type === 'password',
      JSON.stringify(fields),
    );
    await audit(page, 'start page (tr)');
    await shot(page, 'signin-1-start');

    // English and back, without a reload: what was typed stays, and the choice is a cookie. A reload
    // would also drop this marker (router.refresh() updates the history entry, so navigation events
    // cannot tell).
    await page.evaluate(() => {
      window.atiNoReload = true;
    });
    const sameDocument = () => page.evaluate(() => window.atiNoReload === true);
    await page.getByTestId('lang-en').click();
    await page.waitForFunction(() => document.documentElement.lang === 'en');
    await page.getByTestId('lang-en').and(page.locator('[aria-pressed="true"]')).waitFor();
    step('English applies without a reload', (await sameDocument()) && (await number.inputValue()) === NUMBER.toLowerCase());
    const chosen = (await context.cookies()).find((c) => c.name === 'ati_lang');
    step('the choice is kept in ati_lang (Lax, a year)', chosen?.value === 'en' && chosen.sameSite === 'Lax' && chosen.expires - Date.now() / 1000 > 360 * 86400, JSON.stringify(chosen));
    await audit(page, 'start page (en)');
    await shot(page, 'signin-2-english');
    await page.getByTestId('lang-tr').click();
    await page.waitForFunction(() => document.documentElement.lang === 'tr');
    step('back in Turkish, the number still typed', (await sameDocument()) && (await number.inputValue()) === NUMBER.toLowerCase());

    // A malformed number is refused in the page: no request.
    await number.fill('7K3Q-9XW');
    await code.fill(CODE);
    await submit.click();
    await page.getByTestId('sign-in-problem').waitFor();
    step('a malformed number sends nothing', signIns.length === 0, signIns.join(','));
    step('the problem takes focus', (await activeTestId(page)) === 'sign-in-problem');
    step('only the number is marked invalid', (await invalid(number)) && !(await invalid(code)));

    // A wrong code: refused by the server, the same message as any refusal.
    await number.fill(NUMBER);
    await code.fill(WRONG);
    const [refused] = await Promise.all([page.waitForResponse((r) => new URL(r.url()).pathname === SIGN_IN), submit.click()]);
    step('a wrong code is refused', refused.status() === 401, String(refused.status()));
    await page.waitForFunction(() => document.activeElement?.getAttribute('data-testid') === 'sign-in-problem');
    step('the refusal takes focus and marks both fields', (await invalid(number)) && (await invalid(code)));
    step('a refusal opens no session', !(await context.cookies()).some((c) => c.name === 'ati_cs'));
    await audit(page, 'sign-in refused (tr)');
    await shot(page, 'signin-3-refused');

    // The right code, as a person might type it: lower case, a space for the hyphen, shown.
    await page.getByTestId('sign-in-show-code').click();
    step('the code can be shown', (await code.getAttribute('type')) === 'text' && (await page.getByTestId('sign-in-show-code').getAttribute('aria-pressed')) === 'true');
    await code.fill(CODE.toLowerCase().replace('-', ' '));
    const [accepted] = await Promise.all([page.waitForResponse((r) => new URL(r.url()).pathname === SIGN_IN), submit.click()]);
    step('the right code signs in', accepted.status() === 200, String(accepted.status()));
    await page.locator('#consent-heading').waitFor({ timeout: 15_000 });
    result.interview_id = new URL(page.url()).pathname.split('/').filter(Boolean).pop() ?? '';
    const session = (await context.cookies()).find((c) => c.name === 'ati_cs');
    step('the session cookie is HttpOnly, SameSite=Strict, for the API only', session?.httpOnly && session.sameSite === 'Strict' && session.path === '/api');
    appendFileSync(SECRETS, `${secret(session.value)}\n`, { mode: 0o600 });

    // The code is nowhere a page keeps or shows: not in an address, storage, script-readable cookies or the page.
    const kept = await page.evaluate(
      (values) => {
        const places = [location.href, document.cookie, JSON.stringify(Object.entries(localStorage)), JSON.stringify(Object.entries(sessionStorage)), document.documentElement.outerHTML];
        return places.map((place) => values.some((v) => place.toUpperCase().includes(v)));
      },
      [CODE, BARE],
    );
    step('the code is not in the address, storage, cookies or page', !kept.some(Boolean), JSON.stringify(kept));
    step('the code never reached a request address', !requested.some((u) => u.toUpperCase().includes(BARE) || u.toUpperCase().includes(CODE)));

    // The notice, in Turkish, with the application the sign-in matched.
    step('the notice is in Turkish', (await lang(page)) === 'tr');
    step('the application number is shown', ((await page.getByTestId('application-number').textContent()) ?? '').includes(NUMBER));
    step('the interview title is marked as Turkish', (await page.getByTestId('application-title').getAttribute('lang')) === 'tr');
    const current = page.locator('[data-testid="stepper"] [aria-current="step"]');
    step('the steps mark the notice as current', (await current.count()) === 1 && ((await current.textContent()) ?? '').includes('Bilgilendirme'));
    step('the draft notice is labelled TASLAK', ((await page.getByTestId('draft-banner').textContent()) ?? '').includes('TASLAK'));
    await audit(page, 'consent (tr, signed in)');
    await shot(page, 'signin-4-consent');

    // Typed answers, the setup, the room, then withdrawal.
    await consentChoices(page, { voice: false });
    await page.getByTestId('consent-submit').click();
    await page.locator('#setup-heading').waitFor({ timeout: 15_000 });
    step('the steps mark the setup as current', ((await current.textContent()) ?? '').includes('Kurulum'));
    await audit(page, 'setup (tr)');
    await shot(page, 'signin-5-setup');
    await page.getByTestId('start-text').click();
    await page.locator('#room-heading').waitFor({ timeout: 15_000 });
    await audit(page, 'room (tr)');
    await shot(page, 'signin-6-room');
    await withdraw(page);
    step('the interview ended', (await lang(page)) === 'tr');
    await audit(page, 'cancelled (tr)');
  },
  { locale: null, microphone: 'denied' },
);
