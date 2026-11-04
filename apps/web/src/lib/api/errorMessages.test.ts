import { describe, expect, it } from 'vitest';
import { getMessages } from '@/i18n';
import { label } from '@/i18n/label';
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
      // Its own message, not a fallback by status.
      expect(Object.hasOwn(t.errors.byCode, code), code).toBe(true);
      const message = errorMessage(t.errors, new ApiError(409, code));
      // Messages are written for people: no raw snake_case codes.
      if (code.includes('_')) expect(message, code).not.toContain(code);
    }
  });

  it('covers client-side failures', () => {
    for (const code of ['network_error', 'timeout', 'invalid_response']) {
      expect(Object.hasOwn(t.errors.byCode, code), code).toBe(true);
    }
  });

  it('falls back by status, then to a generic message', () => {
    expect(errorMessage(t.errors, new ApiError(429, 'unknown'))).toBe(t.errors.byStatus[429]);
    expect(errorMessage(t.errors, new ApiError(503, 'unknown'))).toBe(t.errors.serverError);
    expect(errorMessage(t.errors, new ApiError(418, 'unknown'))).toBe(t.errors.generic);
  });

  it('cannot be tricked by prototype keys', () => {
    expect(errorMessage(t.errors, new ApiError(400, 'constructor'))).toBe(t.errors.byStatus[400]);
    expect(errorMessage(t.errors, new ApiError(418, '__proto__'))).toBe(t.errors.generic);
  });
});

describe('label', () => {
  it('finds only the keys a catalog has itself', () => {
    expect(label(t.staff.reasons, 'no_rubric')).toBe(t.staff.reasons.no_rubric);
    for (const key of ['constructor', 'toString', '__proto__', 'hasOwnProperty', 'brand_new_reason']) {
      expect(label(t.staff.reasons, key), key).toBeUndefined();
      expect(label(t.consent.basis, key), key).toBeUndefined();
    }
  });
});

/** Codes the review console can receive (control plane: staff session, review and report endpoints). */
const STAFF_CODES = [
  'unauthorized',
  'forbidden',
  'not_found',
  'review_not_open',
  'review_already_completed',
  'rubric_unavailable',
  'invalid_request',
  'rate_limited',
  'origin_mismatch',
  'idempotency_key_required',
  'idempotency_key_reused',
  'unsupported_media_type',
  'payload_too_large',
  'unavailable',
  'internal_error',
];

describe('errorMessage for the review console', () => {
  it('has a specific message for every staff problem code and client-side failure', () => {
    for (const code of [...STAFF_CODES, 'network_error', 'timeout', 'invalid_response']) {
      expect(Object.hasOwn(t.staff.errors.byCode, code), code).toBe(true);
      const message = errorMessage(t.staff.errors, new ApiError(409, code));
      if (code.includes('_')) expect(message, code).not.toContain(code);
    }
  });

  it('never tells staff to open a candidate invitation', () => {
    for (const message of [...Object.values(t.staff.errors.byCode), ...Object.values(t.staff.errors.byStatus)]) {
      expect(message.toLowerCase()).not.toContain('invitation');
    }
  });
});
