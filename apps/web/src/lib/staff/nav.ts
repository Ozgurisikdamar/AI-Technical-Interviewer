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

/** The sign-in page, asked to return to `next` afterwards (if safeNext accepts it then). */
export function signInHref(next: string): string {
  return `${SIGN_IN_PATH}?${new URLSearchParams({ next }).toString()}`;
}

/**
 * Where to go after signing in: `next` when it is a console page this role may open, rebuilt from
 * its parts and equal to them, so nothing else (another site, `//host`, dot segments, encoded or
 * control characters, extra parameters) can pass. Otherwise null, and the caller uses homeFor.
 */
export function safeNext(next: string | undefined, role: StaffRole): string | null {
  if (next === undefined) return null;
  const [path = '', query, ...more] = next.split('?');
  if (more.length > 0) return null;
  const target = ((): { section: StaffSection; href: string } | null => {
    if (path === QUEUE_PATH) {
      const params = new URLSearchParams(query ?? '');
      const { status, interviewId } = queueParams({ status: params.get('status') ?? undefined, interview: params.get('interview') ?? undefined });
      return { section: 'review', href: queueHref(status, interviewId) };
    }
    if (path === REPORTS_PATH) return { section: 'reports', href: REPORTS_PATH };
    const taskId = path.startsWith(`${QUEUE_PATH}/`) ? path.slice(QUEUE_PATH.length + 1) : '';
    if (isReviewTaskId(taskId)) return { section: 'review', href: taskHref(taskId) };
    const interviewId = path.startsWith(`${REPORTS_PATH}/`) ? path.slice(REPORTS_PATH.length + 1) : '';
    if (isInterviewId(interviewId)) return { section: 'reports', href: reportHref(interviewId) };
    return null;
  })();
  return target !== null && target.href === next && sectionsFor(role).includes(target.section) ? target.href : null;
}
