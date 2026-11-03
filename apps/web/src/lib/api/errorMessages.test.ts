import { describe, expect, it } from 'vitest';
import { getMessages } from '@/i18n';
import { errorMessage } from './errorMessages';
import { ApiError } from './errors';

const t = getMessages('en');

/** Stable problem codes the control plane emits (services/control-plane/internal/httpx and interview). */
const SERVER_CODES = [
  'invalid_request',
  'unauthorized',
  'forbidden',
  'not_found',
  'stale_state_version',
  'transition_not_allowed',
  'idempotency_key_required',
  'idempotency_key_reused',
  'rate_limited',
  'payload_too_large',
  'unsupported_media_type',
  'origin_mismatch',
  'internal_error',
  'unavailable',
  'invitation_invalid',
  'retention_policy_missing',
  'plan_unavailable',
  'conflict',
  'voice_processing_not_consented',
  'activation_mismatch',
  'no_pending_request',
  'session_not_live',
];

describe('errorMessage', () => {
  it('has a specific friendly message for every candidate-facing problem code', () => {
    for (const code of SERVER_CODES) {
      const message = errorMessage(t, new ApiError(409, code));
      expect(message, code).not.toBe(t.errors.generic);
      // Messages are written for people: no raw snake_case codes.
      if (code.includes('_')) expect(message, code).not.toContain(code);
    }
  });

  it('covers client-side failures', () => {
    for (const code of ['network_error', 'timeout', 'invalid_response']) {
      expect(errorMessage(t, new ApiError(0, code))).not.toBe(t.errors.generic);
    }
  });

  it('falls back by status, then to a generic message', () => {
    expect(errorMessage(t, new ApiError(429, 'unknown'))).toBe(t.errors.byStatus[429]);
    expect(errorMessage(t, new ApiError(503, 'unknown'))).toBe(t.errors.serverError);
    expect(errorMessage(t, new ApiError(418, 'unknown'))).toBe(t.errors.generic);
  });

  it('cannot be tricked by prototype keys', () => {
    expect(errorMessage(t, new ApiError(400, 'constructor'))).toBe(t.errors.byStatus[400]);
  });
});
