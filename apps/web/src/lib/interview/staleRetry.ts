/**
 * The server rejects state-changing requests that carry an outdated state_version (409
 * stale_state_version). Between two polls the version can legitimately move on without the
 * candidate's intent changing (for example QUESTION_ACTIVE -> CANDIDATE_SPEAKING as soon as an
 * answer starts). In that case the client refetches once and retries with the fresh version,
 * but only if the action still means the same thing; otherwise the conflict is reported.
 */
import { isApiError } from '@/lib/api/errors';
import type { InterviewCandidateView } from '@/lib/api/types';
import { isAllowed } from './viewModel';

export type VersionedAction = 'start' | 'begin' | 'pause' | 'resume' | 'finish_answer' | 'withdraw';

/** Whether retrying `action` against `fresh` still does what the candidate asked for on `original`. */
export function actionStillApplies(
  action: VersionedAction,
  original: InterviewCandidateView,
  fresh: InterviewCandidateView,
): boolean {
  if (fresh.id !== original.id) return false;
  switch (action) {
    case 'start':
      return fresh.state === 'READY' && isAllowed(fresh, 'start');
    case 'begin':
      return fresh.state === 'CALIBRATION' && isAllowed(fresh, 'begin');
    case 'pause':
      return isAllowed(fresh, 'pause');
    case 'resume':
      return fresh.state === 'PAUSED' && isAllowed(fresh, 'resume');
    case 'finish_answer':
      return (
        isAllowed(fresh, 'finish_answer') &&
        !fresh.pending_completion &&
        fresh.active_prompt !== null &&
        fresh.active_prompt.activation_id === original.active_prompt?.activation_id
      );
    case 'withdraw':
      return isAllowed(fresh, 'withdraw');
  }
}

export interface StaleRetryOptions {
  readonly action: VersionedAction;
  readonly fetchLatest: () => Promise<InterviewCandidateView>;
  /** Receives the refetched view (so the UI shows it even if no retry happens). */
  readonly onFresh?: (fresh: InterviewCandidateView) => void;
}

export async function withStaleRetry<T>(
  view: InterviewCandidateView,
  perform: (view: InterviewCandidateView) => Promise<T>,
  options: StaleRetryOptions,
): Promise<T> {
  try {
    return await perform(view);
  } catch (error) {
    if (!isApiError(error) || error.code !== 'stale_state_version') throw error;
    let fresh: InterviewCandidateView;
    try {
      fresh = await options.fetchLatest();
    } catch {
      throw error;
    }
    options.onFresh?.(fresh);
    if (!actionStillApplies(options.action, view, fresh)) throw error;
    return perform(fresh);
  }
}
