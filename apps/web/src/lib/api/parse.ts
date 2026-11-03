/**
 * Runtime validation of API responses. Anything that does not match the contract is rejected
 * (the client turns that into an `invalid_response` ApiError) instead of reaching the UI.
 * Unknown enum values that the contract may add later degrade safely:
 * an unknown state becomes 'UNKNOWN', unknown allowed actions are dropped, an unknown
 * environment is treated as non-production and an unknown notice status as a draft.
 */
import { isActivationId, isInterviewId } from './ids';
import {
  CANDIDATE_ACTIONS,
  ENVIRONMENTS,
  INTERVIEW_STATES,
  type ActivePrompt,
  type CandidateAction,
  type ConsentNotice,
  type ConsentNoticeView,
  type Environment,
  type InterviewCandidateView,
  type InterviewState,
  type InvitationExchangeResponse,
  type NoticePurpose,
  type NoticeSection,
  type PausedFrom,
  type RealtimeSession,
  type RetentionCategory,
  type RetentionDays,
  type TextResponseAccepted,
} from './types';

export class ResponseShapeError extends Error {
  constructor(readonly field: string) {
    super(`Unexpected response shape at ${field}`);
    this.name = 'ResponseShapeError';
  }
}

type Json = Record<string, unknown>;

function record(value: unknown, field: string): Json {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) throw new ResponseShapeError(field);
  return value as Json;
}

function string(obj: Json, key: string, field: string, maxLength = 10_000): string {
  const value = obj[key];
  if (typeof value !== 'string' || value.length > maxLength) throw new ResponseShapeError(`${field}.${key}`);
  return value;
}

function nonEmptyString(obj: Json, key: string, field: string, maxLength = 10_000): string {
  const value = string(obj, key, field, maxLength);
  if (value.length === 0) throw new ResponseShapeError(`${field}.${key}`);
  return value;
}

function integer(obj: Json, key: string, field: string, min: number): number {
  const value = obj[key];
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < min) {
    throw new ResponseShapeError(`${field}.${key}`);
  }
  return value;
}

function boolean(obj: Json, key: string, field: string): boolean {
  const value = obj[key];
  if (typeof value !== 'boolean') throw new ResponseShapeError(`${field}.${key}`);
  return value;
}

function array(obj: Json, key: string, field: string, maxItems = 200): unknown[] {
  const value = obj[key];
  if (!Array.isArray(value) || value.length > maxItems) throw new ResponseShapeError(`${field}.${key}`);
  return value;
}

function state(value: unknown, field: string): InterviewState {
  if (typeof value !== 'string' || !/^[A-Z][A-Z_]{0,39}$/.test(value)) throw new ResponseShapeError(field);
  return (INTERVIEW_STATES as readonly string[]).includes(value) ? (value as InterviewState) : 'UNKNOWN';
}

function activePrompt(value: unknown): ActivePrompt | null {
  if (value === null) return null;
  const obj = record(value, 'active_prompt');
  const activationId = string(obj, 'activation_id', 'active_prompt', 128);
  if (!isActivationId(activationId)) throw new ResponseShapeError('active_prompt.activation_id');
  const kind = obj.kind;
  if (kind !== 'question' && kind !== 'follow_up') throw new ResponseShapeError('active_prompt.kind');
  return { activation_id: activationId, kind, text: string(obj, 'text', 'active_prompt', 5_000) };
}

function pausedFrom(value: unknown): PausedFrom | null {
  return value === 'CALIBRATION' || value === 'QUESTION_ACTIVE' || value === 'CANDIDATE_SPEAKING' ? value : null;
}

function allowedActions(value: unknown): CandidateAction[] {
  if (!Array.isArray(value)) throw new ResponseShapeError('allowed_actions');
  const known = new Set<string>(CANDIDATE_ACTIONS);
  const out: CandidateAction[] = [];
  for (const item of value) {
    if (typeof item === 'string' && known.has(item) && !out.includes(item as CandidateAction)) {
      out.push(item as CandidateAction);
    }
  }
  return out;
}

function environment(value: unknown): Environment | 'unknown' {
  return typeof value === 'string' && (ENVIRONMENTS as readonly string[]).includes(value)
    ? (value as Environment)
    : 'unknown';
}

export function parseCandidateView(body: unknown): InterviewCandidateView {
  const obj = record(body, 'view');
  const id = string(obj, 'id', 'view', 128);
  if (!isInterviewId(id)) throw new ResponseShapeError('view.id');
  const progress = record(obj.progress, 'view.progress');
  const inputMode = obj.input_mode;
  if (inputMode !== 'voice' && inputMode !== 'text' && inputMode !== null) throw new ResponseShapeError('view.input_mode');
  return {
    id,
    state: state(obj.state, 'view.state'),
    state_version: integer(obj, 'state_version', 'view', 1),
    progress: {
      // Display-only: tolerated down to 0 (the contract says >= 1) and clamped when shown,
      // so a cosmetic anomaly never blocks the interview.
      completed_items: integer(progress, 'completed_items', 'view.progress', 0),
      total_items: integer(progress, 'total_items', 'view.progress', 0),
    },
    input_mode: inputMode,
    active_prompt: activePrompt(obj.active_prompt ?? null),
    paused_from: pausedFrom(obj.paused_from),
    pending_completion: boolean(obj, 'pending_completion', 'view'),
    recording_active: boolean(obj, 'recording_active', 'view'),
    allowed_actions: allowedActions(obj.allowed_actions),
    environment: environment(obj.environment),
  };
}

const RETENTION_CATEGORIES: readonly RetentionCategory[] = ['transcript', 'audio_recording', 'session_events', 'assessment_output'];
const PURPOSE_ID = /^[a-z][a-z0-9_]{0,63}$/;

function noticeSection(value: unknown, index: number): NoticeSection {
  const field = `notice.sections[${index}]`;
  const obj = record(value, field);
  return { heading: nonEmptyString(obj, 'heading', field, 200), text: nonEmptyString(obj, 'text', field, 5_000) };
}

function noticePurpose(value: unknown, index: number): NoticePurpose {
  const field = `notice.purposes[${index}]`;
  const obj = record(value, field);
  const purposeId = nonEmptyString(obj, 'purpose_id', field, 64);
  if (!PURPOSE_ID.test(purposeId)) throw new ResponseShapeError(`${field}.purpose_id`);
  const retention = obj.retention_category;
  if (typeof retention !== 'string' || !(RETENTION_CATEGORIES as readonly string[]).includes(retention)) {
    throw new ResponseShapeError(`${field}.retention_category`);
  }
  const categories = array(obj, 'data_categories', field, 20).map((item, i) => {
    if (typeof item !== 'string' || !PURPOSE_ID.test(item)) throw new ResponseShapeError(`${field}.data_categories[${i}]`);
    return item;
  });
  return {
    purpose_id: purposeId,
    required: boolean(obj, 'required', field),
    basis: string(obj, 'basis', field, 64),
    title: nonEmptyString(obj, 'title', field, 200),
    description: nonEmptyString(obj, 'description', field, 3_000),
    data_categories: categories,
    retention_category: retention as RetentionCategory,
  };
}

function notice(value: unknown): ConsentNotice {
  const obj = record(value, 'notice');
  const sections = array(obj, 'sections', 'notice', 50).map(noticeSection);
  const purposes = array(obj, 'purposes', 'notice', 20).map(noticePurpose);
  if (sections.length === 0) throw new ResponseShapeError('notice.sections');
  if (purposes.length === 0) throw new ResponseShapeError('notice.purposes');
  const ids = new Set(purposes.map((p) => p.purpose_id));
  if (ids.size !== purposes.length) throw new ResponseShapeError('notice.purposes (duplicate purpose_id)');
  const language = string(obj, 'language', 'notice', 16);
  return {
    notice_id: nonEmptyString(obj, 'notice_id', 'notice', 128),
    version: nonEmptyString(obj, 'version', 'notice', 64),
    // Fail safe: anything but an explicit approval is presented as a draft.
    status: obj.status === 'approved' ? 'approved' : 'draft',
    language: /^[a-z]{2}(-[A-Za-z]{2,4})?$/.test(language) ? language : 'en',
    title: nonEmptyString(obj, 'title', 'notice', 200),
    sections,
    purposes,
  };
}

function retentionDays(value: unknown): RetentionDays {
  const obj = record(value, 'retention_days');
  const audio = obj.audio_recording;
  if (audio !== null && (typeof audio !== 'number' || !Number.isSafeInteger(audio) || audio < 1)) {
    throw new ResponseShapeError('retention_days.audio_recording');
  }
  return {
    transcript: integer(obj, 'transcript', 'retention_days', 1),
    session_events: integer(obj, 'session_events', 'retention_days', 1),
    assessment_output: integer(obj, 'assessment_output', 'retention_days', 1),
    audio_recording: audio,
  };
}

export function parseConsentNoticeView(body: unknown): ConsentNoticeView {
  const obj = record(body, 'consent_notice');
  return { notice: notice(obj.notice), retention_days: retentionDays(obj.retention_days) };
}

export function parseRealtimeSession(body: unknown): RealtimeSession {
  const obj = record(body, 'realtime_session');
  const url = nonEmptyString(obj, 'livekit_url', 'realtime_session', 2_048);
  let parsed: URL;
  try {
    parsed = new URL(url);
  } catch {
    throw new ResponseShapeError('realtime_session.livekit_url');
  }
  if (!['ws:', 'wss:', 'http:', 'https:'].includes(parsed.protocol) || parsed.username || parsed.password) {
    throw new ResponseShapeError('realtime_session.livekit_url');
  }
  return {
    livekit_url: url,
    token: nonEmptyString(obj, 'token', 'realtime_session', 8_192),
    room: nonEmptyString(obj, 'room', 'realtime_session', 256),
    identity: nonEmptyString(obj, 'identity', 'realtime_session', 128),
    can_publish_audio: boolean(obj, 'can_publish_audio', 'realtime_session'),
    expires_at: string(obj, 'expires_at', 'realtime_session', 64),
  };
}

export function parseExchangeResponse(body: unknown): InvitationExchangeResponse {
  const obj = record(body, 'exchange');
  const interviewId = string(obj, 'interview_id', 'exchange', 128);
  if (!isInterviewId(interviewId)) throw new ResponseShapeError('exchange.interview_id');
  return { interview_id: interviewId, state: state(obj.state, 'exchange.state') };
}

export function parseTextResponseAccepted(body: unknown): TextResponseAccepted {
  const obj = record(body, 'text_response');
  return {
    segment_id: nonEmptyString(obj, 'segment_id', 'text_response', 128),
    state: state(obj.state, 'text_response.state'),
    state_version: integer(obj, 'state_version', 'text_response', 1),
  };
}
