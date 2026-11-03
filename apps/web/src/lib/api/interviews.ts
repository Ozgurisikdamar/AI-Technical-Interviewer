/**
 * Typed wrappers for the candidate endpoints of the public API (/api/v1 only).
 * State-changing calls carry the state_version the client observed (stale versions get 409).
 */
import { getJson, postJson, type RequestOptions } from './client';
import { isActivationId, isInterviewId } from './ids';
import {
  parseCandidateView,
  parseConsentNoticeView,
  parseExchangeResponse,
  parseRealtimeSession,
  parseTextResponseAccepted,
} from './parse';
import type {
  ConsentDecisionRequest,
  ConsentNoticeView,
  InputMode,
  InterviewCandidateView,
  InvitationExchangeResponse,
  PauseReason,
  RealtimeSession,
  TextResponseAccepted,
} from './types';

export const TEXT_RESPONSE_MAX_LENGTH = 4_000;

export function interviewPath(interviewId: string, suffix = ''): string {
  if (!isInterviewId(interviewId)) {
    throw new Error('Invalid interview identifier');
  }
  return `/api/v1/interviews/${encodeURIComponent(interviewId)}${suffix}`;
}

function requireActivationId(activationId: string): string {
  if (!isActivationId(activationId)) throw new Error('Invalid activation identifier');
  return activationId;
}

/** Exchanges the one-time invitation token; the server sets the HttpOnly session cookie. */
export function exchangeInvitation(token: string, options?: RequestOptions): Promise<InvitationExchangeResponse> {
  return postJson('/api/v1/invitations/exchange', { token }, parseExchangeResponse, options);
}

export function getInterview(interviewId: string, options?: RequestOptions): Promise<InterviewCandidateView> {
  return getJson(interviewPath(interviewId), parseCandidateView, options);
}

export function getConsentNotice(interviewId: string, options?: RequestOptions): Promise<ConsentNoticeView> {
  return getJson(interviewPath(interviewId, '/consent-notice'), parseConsentNoticeView, options);
}

export function submitConsent(interviewId: string, body: ConsentDecisionRequest): Promise<InterviewCandidateView> {
  return postJson(interviewPath(interviewId, '/consent'), body, parseCandidateView);
}

export function startInterview(
  interviewId: string,
  stateVersion: number,
  inputMode: InputMode,
): Promise<InterviewCandidateView> {
  return postJson(
    interviewPath(interviewId, '/start'),
    { state_version: stateVersion, input_mode: inputMode },
    parseCandidateView,
  );
}

export function completeCalibration(interviewId: string, stateVersion: number): Promise<InterviewCandidateView> {
  return postJson(interviewPath(interviewId, '/calibration-complete'), { state_version: stateVersion }, parseCandidateView);
}

export function pauseInterview(
  interviewId: string,
  stateVersion: number,
  reason: PauseReason,
): Promise<InterviewCandidateView> {
  return postJson(interviewPath(interviewId, '/pause'), { state_version: stateVersion, reason }, parseCandidateView);
}

export function resumeInterview(interviewId: string, stateVersion: number): Promise<InterviewCandidateView> {
  return postJson(interviewPath(interviewId, '/resume'), { state_version: stateVersion }, parseCandidateView);
}

/** Requests finalization of the open answer (202: resolved after the transcript flush or deadline). */
export function requestAnswerCompletion(
  interviewId: string,
  stateVersion: number,
  activationId: string,
): Promise<InterviewCandidateView> {
  return postJson(
    interviewPath(interviewId, '/answer-complete'),
    { state_version: stateVersion, activation_id: requireActivationId(activationId) },
    parseCandidateView,
  );
}

export function withdrawInterview(interviewId: string, stateVersion: number): Promise<InterviewCandidateView> {
  return postJson(interviewPath(interviewId, '/withdraw'), { state_version: stateVersion, confirm: true }, parseCandidateView);
}

/** Issues a short-lived LiveKit token (no request body in the contract; `{}` is sent as JSON). */
export function issueRealtimeSession(interviewId: string): Promise<RealtimeSession> {
  return postJson(interviewPath(interviewId, '/realtime-session'), {}, parseRealtimeSession);
}

/** Text-mode answer bound to the open activation (ADR-018). Never sent over the LiveKit data channel. */
export function submitTextResponse(
  interviewId: string,
  activationId: string,
  text: string,
): Promise<TextResponseAccepted> {
  if (text.trim().length === 0 || text.length > TEXT_RESPONSE_MAX_LENGTH) {
    throw new Error('Text response length is out of range');
  }
  return postJson(
    interviewPath(interviewId, '/text-responses'),
    { activation_id: requireActivationId(activationId), text },
    parseTextResponseAccepted,
  );
}
