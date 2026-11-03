// Consent refusal (TM section 4): declining a required purpose cancels the interview at once and
// says so; nothing is recorded or handed off. Also checks the document security headers.
import { audit, checkDocumentHeaders, consent, createInvitation, run, step } from './common.mjs';

await run(async ({ page, result }) => {
  const token = await createInvitation();
  const { id, response } = await consent(page, token, { voice: true, transcript: false, ai: true, recording: false });
  result.interview_id = id;
  checkDocumentHeaders(response, 'invitation page');
  // Declining a required purpose asks for confirmation first, explaining what it means.
  const confirm = page.getByTestId('consent-decline-confirm');
  await confirm.waitFor({ timeout: 10_000 });
  step('declining a required purpose is confirmed first', true);
  await audit(page, 'consent (confirm decline)');
  await confirm.click();
  await page.locator('#cancelled-heading').waitFor({ timeout: 15_000 });
  step('a declined required purpose ends the interview', true);
  step('no controls remain', (await page.getByTestId('start-voice').count()) === 0 && (await page.getByTestId('check-mic').count()) === 0);
  await audit(page, 'cancelled');
  const view = await page.evaluate(async (iv) => (await fetch(`/api/v1/interviews/${iv}`)).json(), id);
  step('the server reports CANCELLED with no allowed actions', view.state === 'CANCELLED' && view.allowed_actions.length === 0, JSON.stringify(view));
});
