// Voice flow in the browser: invitation, consent, device check, live room with pause/resume and a
// page refresh, completion. The fake microphone drives the scripted interviewer.
import { WEB, audit, candidateTracks, createInvitation, findCandidateRoom, run, step, steps, until } from './common.mjs';

const COMPLETE_WITHIN_MS = Number(process.env.ATI_E2E_COMPLETE_WITHIN_MS ?? 360_000);

await run(async ({ page, result }) => {
  const token = await createInvitation();
  await page.goto(`${WEB}/i#token=${token}`);
  await page.getByTestId('continue').waitFor();
  step('invitation token removed from the address bar before any exchange', !page.url().includes('token='), page.url());
  step('token not in the DOM', !(await page.content()).includes(token));
  await page.getByTestId('continue').click();

  await page.locator('#consent-heading').waitFor();
  await audit(page, 'consent');
  result.interview_id = new URL(page.url()).pathname.split('/').filter(Boolean).pop() ?? '';
  step('DRAFT notice is labelled', await page.getByTestId('draft-banner').isVisible());
  step('pre-production banner shown', await page.getByTestId('preprod-banner').isVisible());
  for (const [purpose, yes] of [
    ['voice_interview_processing', true],
    ['transcript_assessment_record', true],
    ['ai_assisted_evaluation_with_human_review', true],
    ['audio_recording', false],
  ]) {
    await page.getByTestId(`consent-${purpose}-${yes ? 'yes' : 'no'}`).check();
  }
  await page.getByTestId('consent-submit').click();

  await page.locator('#setup-heading').waitFor();
  await page.getByTestId('check-mic').click();
  await page.waitForFunction(() => !document.querySelector('[data-testid="start-voice"]')?.hasAttribute('disabled'), null, { timeout: 15_000 });
  await audit(page, 'setup (microphone live)');
  await page.getByTestId('start-voice').click();

  await page.locator('#room-heading').waitFor({ timeout: 15_000 });
  const view = await page.evaluate(async (id) => (await fetch(`/api/v1/interviews/${id}`)).json(), result.interview_id);
  step('candidate view has no evaluation data', !JSON.stringify(view).match(/score|rubric|evaluation/i));

  // The interviewer greets; the looping fake microphone answers. Calibration completes on the first words.
  await page.waitForFunction(() => (document.querySelector('[data-testid="prompt"]')?.textContent ?? '').includes('PostgreSQL'), null, {
    timeout: 90_000,
  });
  step('first approved question shown for accessibility', true);
  await audit(page, 'room (voice, question)');

  // Pause: the microphone track disappears from the media server; resume publishes a new one.
  let roomName = null;
  await until(async () => (roomName = await findCandidateRoom()) !== null, 15_000, 'candidate room');
  await until(async () => (await candidateTracks(roomName))?.length === 1, 20_000, 'one microphone track while answering');
  const before = (await candidateTracks(roomName))[0].sid;
  await page.getByTestId('pause').click();
  await page.getByTestId('paused-notice').waitFor();
  await audit(page, 'room (paused)');
  await until(async () => (await candidateTracks(roomName))?.length === 0, 15_000, 'microphone unpublished while paused');
  step('no candidate audio track on the media server while paused', true);
  step('still in the room while paused', (await candidateTracks(roomName)) !== null);
  await page.getByTestId('resume').click();
  await page.getByTestId('paused-notice').waitFor({ state: 'detached', timeout: 15_000 });
  await until(async () => (await candidateTracks(roomName))?.length === 1, 20_000, 'microphone republished after resume');
  step('a new microphone track after resume', (await candidateTracks(roomName))[0].sid !== before);

  // Refresh mid-interview: the page rejoins with a new realtime session; the disconnect paused it.
  await page.waitForTimeout(3_000);
  await page.reload();
  await page.locator('#room-heading').waitFor({ timeout: 20_000 });
  const resume = page.getByTestId('resume');
  if (await resume.isVisible({ timeout: 8_000 }).catch(() => false)) await resume.click();
  steps.push({ name: 'reloaded and rejoined', ok: true });

  await page.locator('#completed-heading').waitFor({ timeout: COMPLETE_WITHIN_MS });
  step('completion page shown', true);
  await audit(page, 'completed');
});
