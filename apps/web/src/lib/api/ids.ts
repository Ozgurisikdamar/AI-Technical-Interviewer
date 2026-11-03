/** Identifier formats from the OpenAPI contract. Checked before an ID is used in a path or a body. */

const INTERVIEW_ID = /^ses_[A-Za-z0-9]{16,64}$/;
const ACTIVATION_ID = /^act_[A-Za-z0-9]{16,64}$/;

export function isInterviewId(value: unknown): value is string {
  return typeof value === 'string' && INTERVIEW_ID.test(value);
}

export function isActivationId(value: unknown): value is string {
  return typeof value === 'string' && ACTIVATION_ID.test(value);
}
