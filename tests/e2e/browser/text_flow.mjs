// Text mode in the browser (ADR-018): the microphone permission is refused, the device check offers
// text mode, and the candidate answers every question in writing. The interviewer still speaks and
// is captioned; the candidate never publishes a track.
import { WEB, audit, candidateTracks, createInvitation, findCandidateRoom, run, step, until } from './common.mjs';

const ANSWERS = [
  'MVCC keeps several versions of a row so that readers never block writers.',
  'Vacuum removes row versions that no transaction can see any more.',
  'An idempotency key stored with the result lets retries return the first outcome.',
  'I would compare latency percentiles per dependency and look for saturation.',
];

await run(
  async ({ page, result }) => {
    const token = await createInvitation();
    await page.goto(`${WEB}/i#token=${token}`);
    await page.getByTestId('continue').click();
    await page.locator('#consent-heading').waitFor();
    result.interview_id = new URL(page.url()).pathname.split('/').filter(Boolean).pop() ?? '';
    for (const [purpose, yes] of [
      ['voice_interview_processing', false],
      ['transcript_assessment_record', true],
      ['ai_assisted_evaluation_with_human_review', true],
      ['audio_recording', false],
    ]) {
      await page.getByTestId(`consent-${purpose}-${yes ? 'yes' : 'no'}`).check();
    }
    await page.getByTestId('consent-submit').click();

    await page.locator('#setup-heading').waitFor();
    const check = page.getByTestId('check-mic');
    if (await check.isVisible().catch(() => false)) {
      await check.click();
      await page.getByTestId('mic-error').waitFor({ timeout: 10_000 }).catch(() => undefined);
    }
    step('text mode is offered without a microphone', await page.getByTestId('start-text').isEnabled());
    await page.getByTestId('start-text').click();

    await page.locator('#room-heading').waitFor({ timeout: 15_000 });
    await page.getByTestId('begin').waitFor({ timeout: 30_000 });
    await page.waitForFunction(() => !document.querySelector('[data-testid="begin"]')?.hasAttribute('disabled'), null, { timeout: 15_000 });
    await page.getByTestId('begin').click();

    let roomName = null;
    await until(async () => (roomName = await findCandidateRoom()) !== null, 15_000, 'candidate room');
    for (const [i, answer] of ANSWERS.entries()) {
      await page.waitForFunction(() => !!document.querySelector('[data-testid="text-input"]'), null, { timeout: 60_000 });
      if (i === 0) {
        await page.waitForFunction(() => (document.querySelector('[data-testid="captions"]')?.textContent ?? '').includes('PostgreSQL'), null, {
          timeout: 30_000,
        });
        step('the interviewer is captioned in text mode', true);
        await audit(page, 'room (text, question)');
      }
      const prompt = await page.getByTestId('prompt').textContent();
      await page.getByTestId('text-input').fill(answer);
      await page.getByTestId('text-send').click();
      await page.waitForFunction(() => (document.querySelector('[data-testid="text-input"]')?.value ?? '') === '', null, { timeout: 15_000 });
      await page.waitForFunction(() => !document.querySelector('[data-testid="finish-answer"]')?.hasAttribute('disabled'), null, { timeout: 15_000 });
      await page.getByTestId('finish-answer').click();
      if (i < ANSWERS.length - 1) {
        await page.waitForFunction((before) => (document.querySelector('[data-testid="prompt"]')?.textContent ?? '') !== before, prompt, {
          timeout: 30_000,
        });
      }
      const tracks = await candidateTracks(roomName);
      step(`no candidate audio track after answer ${i + 1}`, tracks === null || tracks.length === 0, JSON.stringify(tracks));
    }
    await page.locator('#completed-heading').waitFor({ timeout: 60_000 });
    step('completion page shown', true);
    await audit(page, 'completed (text)');
  },
  { microphone: 'denied' },
);
