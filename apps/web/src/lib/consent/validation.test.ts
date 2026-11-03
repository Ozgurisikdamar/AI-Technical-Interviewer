import { describe, expect, it } from 'vitest';
import type { ConsentNotice, NoticePurpose } from '@/lib/api/types';
import { buildConsentRequest, validateConsent } from './validation';

function purpose(purpose_id: string, required: boolean): NoticePurpose {
  return {
    purpose_id,
    required,
    basis: 'to_be_determined_by_legal',
    title: purpose_id,
    description: 'd',
    data_categories: ['transcript'],
    retention_category: 'transcript',
  };
}

const notice: ConsentNotice = {
  notice_id: 'notice_default',
  version: '2026-11-draft.1',
  status: 'draft',
  language: 'en',
  title: 'T',
  sections: [{ heading: 'H', text: 'X' }],
  purposes: [
    purpose('voice_interview_processing', false),
    purpose('transcript_assessment_record', true),
    purpose('ai_assisted_evaluation_with_human_review', true),
    purpose('audio_recording', false),
  ],
};

const allYes = {
  voice_interview_processing: true,
  transcript_assessment_record: true,
  ai_assisted_evaluation_with_human_review: true,
  audio_recording: true,
};

describe('validateConsent', () => {
  it('requires an explicit decision for every purpose (nothing is pre-selected)', () => {
    const v = validateConsent(notice.purposes, {});
    expect(v.complete).toBe(false);
    expect(v.missing).toEqual(notice.purposes.map((p) => p.purpose_id));
  });

  it('counts "No" as a decision, not as missing', () => {
    const v = validateConsent(notice.purposes, { ...allYes, audio_recording: false });
    expect(v.complete).toBe(true);
    expect(v.missing).toEqual([]);
    expect(v.declinedRequired).toEqual([]);
  });

  it('lists only the purposes still undecided, in notice order', () => {
    const v = validateConsent(notice.purposes, { audio_recording: false, transcript_assessment_record: true });
    expect(v.missing).toEqual(['voice_interview_processing', 'ai_assisted_evaluation_with_human_review']);
  });

  it('flags declined required purposes (the interview would be cancelled)', () => {
    const v = validateConsent(notice.purposes, { ...allYes, ai_assisted_evaluation_with_human_review: false });
    expect(v.declinedRequired).toEqual(['ai_assisted_evaluation_with_human_review']);
  });

  it('detects a declined voice purpose (text mode remains available)', () => {
    expect(validateConsent(notice.purposes, { ...allYes, voice_interview_processing: false }).voiceDeclined).toBe(true);
    expect(validateConsent(notice.purposes, allYes).voiceDeclined).toBe(false);
  });

  it('ignores decisions for purposes that are not in the notice', () => {
    const v = validateConsent(notice.purposes, { ...allYes, marketing: true } as Record<string, boolean>);
    expect(v.complete).toBe(true);
  });

  it('is not fooled by inherited object properties', () => {
    const v = validateConsent([purpose('toString', true)], {});
    expect(v.missing).toEqual(['toString']);
  });
});

describe('buildConsentRequest', () => {
  it('pins the displayed notice version and sends one decision per purpose in order', () => {
    const body = buildConsentRequest(notice, { ...allYes, audio_recording: false, marketing: true } as Record<string, boolean>);
    expect(body).toEqual({
      notice_id: 'notice_default',
      notice_version: '2026-11-draft.1',
      decisions: [
        { purpose_id: 'voice_interview_processing', granted: true },
        { purpose_id: 'transcript_assessment_record', granted: true },
        { purpose_id: 'ai_assisted_evaluation_with_human_review', granted: true },
        { purpose_id: 'audio_recording', granted: false },
      ],
    });
  });

  it('refuses to build a request while any purpose is undecided', () => {
    expect(() => buildConsentRequest(notice, { voice_interview_processing: true })).toThrow();
  });
});
