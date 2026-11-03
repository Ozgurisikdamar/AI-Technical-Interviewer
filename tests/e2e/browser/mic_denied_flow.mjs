// QA-AN-01: the candidate agreed to voice processing but the browser refuses the microphone. The
// device check explains it, voice cannot start, and text mode takes over (ADR-018).
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
    step('the refusal is explained', /blocked|allow microphone/i.test(text), text);
    step('voice cannot start without a microphone', await page.getByTestId('start-voice').isDisabled());
    step('text mode is offered', await page.getByTestId('start-text').isEnabled());
    await audit(page, 'setup (microphone refused)');
    await page.getByTestId('start-text').click();

    await page.locator('#room-heading').waitFor({ timeout: 15_000 });
    await page.getByTestId('begin').waitFor({ timeout: 30_000 });
    await page.waitForFunction(() => !document.querySelector('[data-testid="begin"]')?.hasAttribute('disabled'), null, { timeout: 15_000 });
    await page.getByTestId('begin').click();
    await page.waitForFunction(() => !!document.querySelector('[data-testid="text-input"]'), null, { timeout: 60_000 });
    await audit(page, 'room (text mode)');
    const prompt = await page.getByTestId('prompt').textContent();
    await page.getByTestId('text-input').fill('MVCC keeps row versions so that readers never block writers.');
    await page.getByTestId('text-send').click();
    await page.waitForFunction(() => !document.querySelector('[data-testid="finish-answer"]')?.hasAttribute('disabled'), null, { timeout: 15_000 });
    await page.getByTestId('finish-answer').click();
    await page.waitForFunction((before) => (document.querySelector('[data-testid="prompt"]')?.textContent ?? '') !== before, prompt, {
      timeout: 30_000,
    });
    step('the written answer moved the interview to the next question', true);
    await withdraw(page);
    await audit(page, 'cancelled');
  },
  { microphone: 'denied' },
);
