// Keyboard-only flow with microphone loss and withdrawal: every control is reached with Tab and
// operated with Enter or Space; losing the microphone pauses the interview (device_lost) and stops
// publishing; withdrawal is confirmed through the dialog, whose safe choice has focus first.
import { WEB, audit, candidateTracks, createInvitation, findCandidateRoom, run, step, until } from './common.mjs';

/** Presses Tab until the element with the test id has focus; fails if it never does. */
async function tabTo(page, testId, maxTabs = 40) {
  for (let i = 0; i < maxTabs; i++) {
    if ((await page.evaluate(() => document.activeElement?.getAttribute('data-testid'))) === testId) return;
    await page.keyboard.press('Tab');
  }
  throw new Error(`could not reach ${testId} with the keyboard`);
}

await run(async ({ page, context, result }) => {
  // Keep a handle on every capture track so the test can simulate the device going away.
  await context.addInitScript(() => {
    const original = navigator.mediaDevices.getUserMedia.bind(navigator.mediaDevices);
    window.__atiMicTracks = [];
    navigator.mediaDevices.getUserMedia = async (constraints) => {
      const stream = await original(constraints);
      window.__atiMicTracks.push(...stream.getAudioTracks());
      return stream;
    };
  });

  const token = await createInvitation();
  await page.goto(`${WEB}/i#token=${token}`);
  await page.getByTestId('continue').waitFor();
  await tabTo(page, 'continue');
  await page.keyboard.press('Enter');

  await page.locator('#consent-heading').waitFor();
  result.interview_id = new URL(page.url()).pathname.split('/').filter(Boolean).pop() ?? '';
  for (const [purpose, yes] of [
    ['voice_interview_processing', true],
    ['transcript_assessment_record', true],
    ['ai_assisted_evaluation_with_human_review', true],
    ['audio_recording', false],
  ]) {
    const id = `consent-${purpose}-${yes ? 'yes' : 'no'}`;
    await tabTo(page, `consent-${purpose}-yes`); // a radio group is one tab stop
    if (!yes) await page.keyboard.press('ArrowDown');
    await page.keyboard.press('Space');
    step(`consent ${purpose} set by keyboard`, await page.getByTestId(id).isChecked());
  }
  await tabTo(page, 'consent-submit');
  await page.keyboard.press('Enter');

  await page.locator('#setup-heading').waitFor();
  await tabTo(page, 'check-mic');
  await page.keyboard.press('Enter');
  await page.waitForFunction(() => !document.querySelector('[data-testid="start-voice"]')?.hasAttribute('disabled'), null, { timeout: 15_000 });
  await tabTo(page, 'start-voice');
  await page.keyboard.press('Enter');

  await page.locator('#room-heading').waitFor({ timeout: 15_000 });
  step('state changes are announced to assistive technology',
    (await page.getByTestId('announcer').getAttribute('aria-live')) === 'polite');
  await page.waitForFunction(() => (document.querySelector('[data-testid="prompt"]')?.textContent ?? '').includes('PostgreSQL'), null, {
    timeout: 90_000,
  });

  // The microphone goes away mid-interview.
  let roomName = null;
  await until(async () => (roomName = await findCandidateRoom()) !== null, 15_000, 'candidate room');
  await until(async () => (await candidateTracks(roomName))?.length === 1, 20_000, 'microphone published');
  await page.evaluate(() => {
    for (const t of window.__atiMicTracks) {
      t.stop();
      t.dispatchEvent(new Event('ended'));
    }
  });
  await page.getByTestId('paused-notice').waitFor({ timeout: 15_000 });
  await audit(page, 'room (device lost)');
  await until(async () => (await candidateTracks(roomName))?.length === 0, 15_000, 'microphone unpublished after device loss');
  step('device loss paused the interview and stopped publishing', true);
  const view = await page.evaluate(async (id) => (await fetch(`/api/v1/interviews/${id}`)).json(), result.interview_id);
  step('the server is paused', view.state === 'PAUSED', view.state);

  // Resume by keyboard: the microphone is acquired again.
  await tabTo(page, 'resume');
  await page.keyboard.press('Enter');
  await until(async () => (await candidateTracks(roomName))?.length === 1, 20_000, 'microphone republished after resume');
  step('resumed by keyboard with a new microphone track', true);

  // Withdraw by keyboard: the dialog opens with the safe choice focused.
  await tabTo(page, 'withdraw');
  await page.keyboard.press('Enter');
  await page.waitForFunction(() => document.querySelector('dialog')?.open === true, null, { timeout: 5_000 });
  await audit(page, 'withdraw dialog');
  const focused = await page.evaluate(() => document.activeElement?.getAttribute('data-testid') ?? document.activeElement?.textContent);
  step('withdrawal dialog focuses the safe choice first', focused !== 'withdraw-confirm', String(focused));
  await tabTo(page, 'withdraw-confirm', 10);
  await page.keyboard.press('Enter');
  await page.locator('#cancelled-heading').waitFor({ timeout: 15_000 });
  await until(async () => (await candidateTracks(roomName)) === null, 20_000, 'candidate left the room after withdrawal');
  step('withdrawn and out of the room', true);
});
