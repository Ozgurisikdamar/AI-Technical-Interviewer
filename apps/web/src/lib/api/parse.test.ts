import { existsSync, readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { parseCandidateView, parseConsentNoticeView, parseExchangeResponse, parseRealtimeSession } from './parse';

const ID = 'ses_ABCDEFGHIJKLMNOPQRSTUV';
const base = {
  id: ID,
  state: 'READY',
  state_version: 3,
  progress: { completed_items: 0, total_items: 3 },
  input_mode: null,
  active_prompt: null,
  paused_from: null,
  pending_completion: false,
  recording_active: false,
  allowed_actions: ['start', 'withdraw'],
  environment: 'test',
};

describe('parseCandidateView', () => {
  it('accepts a contract-shaped view', () => {
    expect(parseCandidateView(base)).toEqual(base);
  });

  it('maps states this build does not know to UNKNOWN instead of failing', () => {
    expect(parseCandidateView({ ...base, state: 'SOMETHING_NEW' }).state).toBe('UNKNOWN');
  });

  it('drops unknown or duplicate allowed actions', () => {
    const view = parseCandidateView({ ...base, allowed_actions: ['start', 'score', 'start', 'publish_data', 7] });
    expect(view.allowed_actions).toEqual(['start']);
  });

  it('treats an unknown environment as non-production', () => {
    expect(parseCandidateView({ ...base, environment: 'prod' }).environment).toBe('unknown');
  });

  it.each([
    ['id', { id: '../../internal' }],
    ['state_version', { state_version: 0 }],
    ['state_version', { state_version: 1.5 }],
    ['input_mode', { input_mode: 'video' }],
    ['progress', { progress: { completed_items: -1, total_items: 3 } }],
    ['pending_completion', { pending_completion: 'yes' }],
    ['allowed_actions', { allowed_actions: 'start' }],
    ['active_prompt', { active_prompt: { activation_id: 'act_bad', kind: 'question', text: 'x' } }],
    ['active_prompt kind', { active_prompt: { activation_id: 'act_ABCDEFGHIJKLMNOPQRSTUV', kind: 'score', text: 'x' } }],
    ['state', { state: 'lower' }],
  ])('rejects an invalid %s', (_field, patch) => {
    expect(() => parseCandidateView({ ...base, ...patch })).toThrow();
  });
});

describe('parseConsentNoticeView', () => {
  const noticePath = fileURLToPath(
    new URL('../../../../../contracts/content/notices/notice_default.2026-11-draft.1.en.json', import.meta.url),
  );

  it.skipIf(!existsSync(noticePath))('accepts the repository default notice (contract drift check)', () => {
    const notice: unknown = JSON.parse(readFileSync(noticePath, 'utf8'));
    const parsed = parseConsentNoticeView({
      notice,
      retention_days: { transcript: 180, session_events: 90, assessment_output: 365, audio_recording: null },
    });
    expect(parsed.notice.status).toBe('draft');
    expect(parsed.notice.purposes.map((p) => p.purpose_id)).toEqual([
      'voice_interview_processing',
      'transcript_assessment_record',
      'ai_assisted_evaluation_with_human_review',
      'audio_recording',
    ]);
    const required = parsed.notice.purposes.filter((p) => p.required).map((p) => p.purpose_id);
    expect(required).toEqual(['transcript_assessment_record', 'ai_assisted_evaluation_with_human_review']);
    expect(parsed.retention_days.audio_recording).toBeNull();
  });

  const minimal = {
    notice: {
      notice_id: 'notice_default',
      version: '2026-11-draft.1',
      status: 'approved',
      language: 'tr',
      title: 'T',
      sections: [{ heading: 'H', text: 'X' }],
      purposes: [
        {
          purpose_id: 'transcript_assessment_record',
          required: true,
          basis: 'consent',
          title: 'P',
          description: 'D',
          data_categories: ['transcript'],
          retention_category: 'transcript',
        },
      ],
    },
    retention_days: { transcript: 30, session_events: 30, assessment_output: 30, audio_recording: 14 },
  };

  it('treats any status other than "approved" as a draft (fail safe)', () => {
    const parsed = parseConsentNoticeView({ ...minimal, notice: { ...minimal.notice, status: 'final' } });
    expect(parsed.notice.status).toBe('draft');
    expect(parseConsentNoticeView(minimal).notice.status).toBe('approved');
  });

  it('rejects duplicate purposes, empty purpose lists and bad retention', () => {
    const purpose = minimal.notice.purposes[0];
    expect(() =>
      parseConsentNoticeView({ ...minimal, notice: { ...minimal.notice, purposes: [purpose, purpose] } }),
    ).toThrow();
    expect(() => parseConsentNoticeView({ ...minimal, notice: { ...minimal.notice, purposes: [] } })).toThrow();
    expect(() =>
      parseConsentNoticeView({ ...minimal, retention_days: { ...minimal.retention_days, transcript: 0 } }),
    ).toThrow();
  });
});

describe('parseRealtimeSession', () => {
  const session = {
    livekit_url: 'wss://lk.example.invalid',
    token: 'test-only-livekit-token',
    room: 'rm_x',
    identity: 'candidate',
    can_publish_audio: true,
    expires_at: '2026-11-03T12:00:00Z',
  };

  it('accepts ws(s) and http(s) LiveKit URLs', () => {
    expect(parseRealtimeSession(session).can_publish_audio).toBe(true);
    expect(parseRealtimeSession({ ...session, livekit_url: 'ws://localhost:7880' }).livekit_url).toBe('ws://localhost:7880');
  });

  it.each(['javascript:alert(1)', 'file:///etc/passwd', 'not a url', 'wss://user:pass@lk.example.invalid'])(
    'rejects %s',
    (url) => {
      expect(() => parseRealtimeSession({ ...session, livekit_url: url })).toThrow();
    },
  );

  it('rejects an empty token', () => {
    expect(() => parseRealtimeSession({ ...session, token: '' })).toThrow();
  });
});

describe('parseExchangeResponse', () => {
  it('only accepts a well-formed interview id (it becomes a navigation target)', () => {
    expect(parseExchangeResponse({ interview_id: ID, state: 'CONSENT_PENDING' }).interview_id).toBe(ID);
    expect(() => parseExchangeResponse({ interview_id: '//evil.example.invalid', state: 'CREATED' })).toThrow();
  });
});
