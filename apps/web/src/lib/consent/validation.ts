/**
 * Purpose-level consent (consent-notice.schema.json). Every purpose of the pinned notice needs an
 * explicit decision; nothing is pre-selected. Declining a required purpose cancels the interview
 * (T03); declining the live voice purpose still allows text responses (ADR-018).
 */
import type { ConsentDecisionRequest, ConsentNotice, NoticePurpose } from '@/lib/api/types';

export const VOICE_PURPOSE_ID = 'voice_interview_processing';
export const RECORDING_PURPOSE_ID = 'audio_recording';

/** undefined = not decided yet. */
export type Decisions = Readonly<Record<string, boolean | undefined>>;

export interface ConsentValidation {
  /** Every purpose has an explicit decision. */
  readonly complete: boolean;
  /** Purposes still undecided, in notice order. */
  readonly missing: readonly string[];
  /** Required purposes the candidate declined (submitting cancels the interview). */
  readonly declinedRequired: readonly string[];
  /** The live voice purpose was declined: the interview can only use text responses. */
  readonly voiceDeclined: boolean;
}

export function validateConsent(purposes: readonly NoticePurpose[], decisions: Decisions): ConsentValidation {
  const missing: string[] = [];
  const declinedRequired: string[] = [];
  for (const purpose of purposes) {
    const decision = Object.prototype.hasOwnProperty.call(decisions, purpose.purpose_id)
      ? decisions[purpose.purpose_id]
      : undefined;
    if (decision === undefined) {
      missing.push(purpose.purpose_id);
    } else if (!decision && purpose.required) {
      declinedRequired.push(purpose.purpose_id);
    }
  }
  const voicePurpose = purposes.some((p) => p.purpose_id === VOICE_PURPOSE_ID);
  return {
    complete: missing.length === 0,
    missing,
    declinedRequired,
    voiceDeclined: voicePurpose && decisions[VOICE_PURPOSE_ID] === false,
  };
}

/**
 * Builds the request body: one decision per notice purpose, in notice order, pinned to the exact
 * notice version that was displayed. Throws if any purpose is undecided.
 */
export function buildConsentRequest(notice: ConsentNotice, decisions: Decisions): ConsentDecisionRequest {
  const validation = validateConsent(notice.purposes, decisions);
  if (!validation.complete) {
    throw new Error('Every purpose needs an explicit decision');
  }
  return {
    notice_id: notice.notice_id,
    notice_version: notice.version,
    decisions: notice.purposes.map((purpose) => ({
      purpose_id: purpose.purpose_id,
      granted: decisions[purpose.purpose_id] === true,
    })),
  };
}
