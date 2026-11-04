/**
 * Review console addresses and what each role may open. Recruiters read reports; reviewers and
 * administrators also review. The server enforces the same rules; this only shapes the UI.
 */
import { isInterviewId, isReviewTaskId } from '@/lib/api/ids';
import type { ReviewStatus, StaffRole } from '@/lib/api/staffTypes';

export const SIGN_IN_PATH = '/staff';
export const QUEUE_PATH = '/staff/review';
export const REPORTS_PATH = '/staff/reports';

export type StaffSection = 'review' | 'reports';

export function canReview(role: StaffRole): boolean {
  return role === 'reviewer' || role === 'admin';
}

export function sectionsFor(role: StaffRole): StaffSection[] {
  return canReview(role) ? ['review', 'reports'] : ['reports'];
}

/** Where a staff member lands after signing in. */
export function homeFor(role: StaffRole): string {
  return canReview(role) ? QUEUE_PATH : REPORTS_PATH;
}

export function queueHref(status: ReviewStatus = 'open', interviewId?: string): string {
  const params = new URLSearchParams();
  if (status !== 'open') params.set('status', status);
  if (interviewId !== undefined && isInterviewId(interviewId)) params.set('interview', interviewId);
  const query = params.toString();
  return query === '' ? QUEUE_PATH : `${QUEUE_PATH}?${query}`;
}

export function taskHref(taskId: string): string {
  if (!isReviewTaskId(taskId)) throw new Error('Invalid review task identifier');
  return `${QUEUE_PATH}/${taskId}`;
}

export function reportHref(interviewId: string): string {
  if (!isInterviewId(interviewId)) throw new Error('Invalid interview identifier');
  return `${REPORTS_PATH}/${interviewId}`;
}

/** Reads the queue page's search parameters; anything unexpected falls back to the defaults. */
export function queueParams(search: { status?: string | string[]; interview?: string | string[] }): {
  status: ReviewStatus;
  interviewId: string | undefined;
} {
  const status = search.status === 'completed' ? 'completed' : 'open';
  const interviewId = typeof search.interview === 'string' && isInterviewId(search.interview) ? search.interview : undefined;
  return { status, interviewId };
}
