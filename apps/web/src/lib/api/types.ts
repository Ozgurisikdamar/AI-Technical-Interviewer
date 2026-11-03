/**
 * Types for the public control-plane API (docs/contracts/openapi/control-plane.v1.yaml, /api/v1 only).
 * Responses are validated at runtime in ./parse.ts before they reach the UI.
 */

export const INTERVIEW_STATES = [
  'CREATED',
  'CONSENT_PENDING',
  'READY',
  'CALIBRATION',
  'QUESTION_ACTIVE',
  'CANDIDATE_SPEAKING',
  'PAUSED',
  'INTERVIEW_COMPLETED',
  'EVALUATION_PENDING',
  'HUMAN_REVIEW',
  'REPORT_READY',
  'CANCELLED',
] as const;

export type KnownInterviewState = (typeof INTERVIEW_STATES)[number];
/** A state this build does not know (the contract may grow); rendered as a neutral waiting view. */
export type InterviewState = KnownInterviewState | 'UNKNOWN';

export const CANDIDATE_ACTIONS = [
  'consent',
  'start',
  'begin',
  'pause',
  'resume',
  'finish_answer',
  'withdraw',
  'text_response',
  'join_media',
] as const;

export type CandidateAction = (typeof CANDIDATE_ACTIONS)[number];

export type InputMode = 'voice' | 'text';

export const ENVIRONMENTS = ['dev', 'test', 'staging', 'production'] as const;
export type Environment = (typeof ENVIRONMENTS)[number];

export interface Progress {
  completed_items: number;
  total_items: number;
}

export interface ActivePrompt {
  activation_id: string;
  kind: 'question' | 'follow_up';
  /** Displayed for accessibility; identical to what the interviewer is asked to say. */
  text: string;
}

export type PausedFrom = 'CALIBRATION' | 'QUESTION_ACTIVE' | 'CANDIDATE_SPEAKING';

export interface InterviewCandidateView {
  id: string;
  state: InterviewState;
  state_version: number;
  progress: Progress;
  input_mode: InputMode | null;
  active_prompt: ActivePrompt | null;
  paused_from: PausedFrom | null;
  pending_completion: boolean;
  recording_active: boolean;
  allowed_actions: readonly CandidateAction[];
  /** 'unknown' when the server sent a value this build does not recognise (treated as non-production). */
  environment: Environment | 'unknown';
}

export type RetentionCategory = 'transcript' | 'audio_recording' | 'session_events' | 'assessment_output';

export interface NoticeSection {
  heading: string;
  text: string;
}

export interface NoticePurpose {
  purpose_id: string;
  required: boolean;
  basis: string;
  title: string;
  description: string;
  data_categories: readonly string[];
  retention_category: RetentionCategory;
}

export interface ConsentNotice {
  notice_id: string;
  version: string;
  status: 'draft' | 'approved';
  language: string;
  title: string;
  sections: readonly NoticeSection[];
  purposes: readonly NoticePurpose[];
}

export interface RetentionDays {
  transcript: number;
  session_events: number;
  assessment_output: number;
  audio_recording: number | null;
}

export interface ConsentNoticeView {
  notice: ConsentNotice;
  retention_days: RetentionDays;
}

export interface ConsentDecision {
  purpose_id: string;
  granted: boolean;
}

export interface ConsentDecisionRequest {
  notice_id: string;
  notice_version: string;
  decisions: ConsentDecision[];
}

export interface RealtimeSession {
  livekit_url: string;
  token: string;
  room: string;
  /** Always "candidate" per the contract; not relied upon (the client uses the joined identity). */
  identity: string;
  can_publish_audio: boolean;
  expires_at: string;
}

export interface InvitationExchangeResponse {
  interview_id: string;
  state: InterviewState;
}

export interface TextResponseAccepted {
  segment_id: string;
  state: InterviewState;
  state_version: number;
}

export type PauseReason = 'candidate_request' | 'device_lost';
