// QA-AN-02: no microphone exists at all (permission granted, no capture device). The device check
// says so and offers text mode; voice cannot start.
import { audit, consent, createInvitation, run, step, withdraw } from './common.mjs';

await run(
  async ({ page, result }) => {
    const token = await createInvitation();
    const { id } = await consent(page, token, { voice: true });
    result.interview_id = id;
    await page.locator('#setup-heading').waitFor();
    await page.getByTestId('check-mic').click();
    const status = page.getByTestId('mic-status');
    await status.getByRole('alert').waitFor({ timeout: 15_000 });
    const text = (await status.textContent()) ?? '';
    step('a missing microphone is explained', /no microphone was found/i.test(text), text);
    step('voice cannot start', await page.getByTestId('start-voice').isDisabled());
    step('text mode is offered', await page.getByTestId('start-text').isEnabled());
    await audit(page, 'setup (no device)');
    await page.getByTestId('start-text').click();
    await page.locator('#room-heading').waitFor({ timeout: 15_000 });
    step('the interview continues in text mode', true);
    await withdraw(page);
  },
  { device: false },
);
