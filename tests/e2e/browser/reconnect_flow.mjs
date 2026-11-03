// QA-AN-08 made deterministic: the candidate leaves the room page (connection gone), the media
// server reports it and the interview pauses as "network"; returning to the page rejoins the room
// and resumes as a new activation of the same question. A page refresh that rejoins before the
// webhook arrives does not pause at all (voice_flow.mjs), which is intended.
// Blocked autoplay (the enable-audio control) cannot be reproduced here: automated Chromium grants
// a user activation on every scripted evaluation. It is covered by the realtime controller tests.
import { audit, candidateTracks, checkDocumentHeaders, consent, createInvitation, findCandidateRoom, run, sleep, step, until, withdraw } from './common.mjs';

await run(
  async ({ page, result }) => {
    const token = await createInvitation();
    const { id } = await consent(page, token, { voice: true });
    result.interview_id = id;
    await page.locator('#setup-heading').waitFor();
    await page.getByTestId('check-mic').click();
    await page.waitForFunction(() => !document.querySelector('[data-testid="start-voice"]')?.hasAttribute('disabled'), null, { timeout: 15_000 });
    await page.getByTestId('start-voice').click(); // a user gesture: audio may play on this document
    await page.locator('#room-heading').waitFor({ timeout: 15_000 });
    const roomUrl = page.url();
    await page.waitForFunction(() => (document.querySelector('[data-testid="prompt"]')?.textContent ?? '').includes('PostgreSQL'), null, {
      timeout: 90_000,
    });
    const question = await page.getByTestId('prompt').textContent();
    await audit(page, 'room (voice)');
    let room = null;
    await until(async () => (room = await findCandidateRoom()) !== null, 15_000, 'candidate room');

    await page.goto('about:blank');
    await until(async () => (await candidateTracks(room)) === null, 15_000, 'the candidate left the media room');
    const view = async () => {
      const r = await fetch(`${process.env.ATI_E2E_PUBLIC_URL}/api/v1/interviews/${id}`, {
        headers: { Authorization: `Bearer ${process.env.ATI_E2E_STAFF_TOKEN}` },
      });
      return r.json();
    };
    await until(async () => (await view()).state === 'PAUSED', 15_000, 'paused after the disconnect');
    step('leaving the page pauses the interview', true);

    const back = await page.goto(roomUrl);
    checkDocumentHeaders(back, 'interview page');
    await page.getByTestId('paused-notice').waitFor({ timeout: 20_000 });
    step('the returning page shows the pause', true);
    await until(async () => (await candidateTracks(room)) !== null, 20_000, 'rejoined the media room while paused');
    step('the returning page rejoins the media room while paused', (await candidateTracks(room))?.length === 0);
    await audit(page, 'paused (returned)');

    await page.getByTestId('resume').click();
    await page.getByTestId('paused-notice').waitFor({ state: 'detached', timeout: 15_000 });
    await until(async () => (await candidateTracks(room))?.length === 1, 20_000, 'microphone published after resume');
    await sleep(1_000);
    step('the same question is asked again', (await page.getByTestId('prompt').textContent()) === question);
    await withdraw(page);
  },
);
