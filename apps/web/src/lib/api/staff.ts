/**
 * Typed wrappers for the review console's endpoints (/api/v1 only). The browser session is an
 * HttpOnly cookie the control plane sets at sign-in (ADR-023): this code never sees it, and the
 * staff token it sends once is never stored.
 */
import { getJson, postJson, type RequestOptions } from './client';
import { isInterviewId, isReviewTaskId } from './ids';
import { interviewPath } from './interviews';
import {
  parseReport,
  parseReportList,
  parseReviewOutcomeResult,
  parseReviewTask,
  parseReviewTaskList,
  parseSessionEnded,
  parseStaffSession,
} from './staffParse';
import {
  PAGE_LIMIT,
  type Report,
  type ReportSummary,
  type ReviewDecision,
  type ReviewOutcomeResult,
  type ReviewStatus,
  type ReviewTask,
  type ReviewTaskSummary,
  type StaffSession,
} from './staffTypes';

function taskPath(taskId: string, suffix = ''): string {
  if (!isReviewTaskId(taskId)) throw new Error('Invalid review task identifier');
  return `/api/v1/review-tasks/${encodeURIComponent(taskId)}${suffix}`;
}

/** Exchanges a staff token for the session cookie. Malformed, unknown, expired and revoked tokens get the same 401. */
export function signIn(token: string, options?: RequestOptions): Promise<StaffSession> {
  return postJson('/api/v1/staff/session', { token }, parseStaffSession, options);
}

export function getStaffSession(options?: RequestOptions): Promise<StaffSession> {
  return getJson('/api/v1/staff/session', parseStaffSession, options);
}

export function signOut(options?: RequestOptions): Promise<{ ended: boolean }> {
  return postJson('/api/v1/staff/session/end', {}, parseSessionEnded, options);
}

export interface QueueFilter {
  readonly status: ReviewStatus;
  /** Only this interview's tasks. */
  readonly interviewId?: string;
}

/** Lists ask for the largest page; a list of PAGE_LIMIT entries may have been cut off. */
export function listReviewTasks(filter: QueueFilter, options?: RequestOptions): Promise<ReviewTaskSummary[]> {
  if (filter.interviewId !== undefined && !isInterviewId(filter.interviewId)) throw new Error('Invalid interview identifier');
  return getJson('/api/v1/review-tasks', parseReviewTaskList, {
    ...options,
    query: { status: filter.status, interview_id: filter.interviewId, limit: PAGE_LIMIT },
  });
}

export function getReviewTask(taskId: string, options?: RequestOptions): Promise<ReviewTask> {
  return getJson(taskPath(taskId), parseReviewTask, options);
}

export function submitReviewOutcome(taskId: string, decision: ReviewDecision, options?: RequestOptions): Promise<ReviewOutcomeResult> {
  return postJson(taskPath(taskId, '/outcome'), decision, parseReviewOutcomeResult, options);
}

export function listReports(options?: RequestOptions): Promise<ReportSummary[]> {
  return getJson('/api/v1/reports', parseReportList, { ...options, query: { limit: PAGE_LIMIT } });
}

export function getReport(interviewId: string, options?: RequestOptions): Promise<Report> {
  return getJson(interviewPath(interviewId, '/report'), parseReport, options);
}
