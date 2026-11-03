import { describe, expect, it, vi } from 'vitest';
import { ApiError } from '@/lib/api/errors';
import type { CandidateAction, InterviewCandidateView, InterviewState } from '@/lib/api/types';
import { actionStillApplies, withStaleRetry } from './staleRetry';

const ACT1 = 'act_AAAAAAAAAAAAAAAAAAAAAA';
const ACT2 = 'act_BBBBBBBBBBBBBBBBBBBBBB';

function view(state: InterviewState, version: number, actions: CandidateAction[], activation = ACT1): InterviewCandidateView {
  return {
    id: 'ses_ABCDEFGHIJKLMNOPQRSTUV',
    state,
    state_version: version,
    progress: { completed_items: 0, total_items: 2 },
    input_mode: 'voice',
    active_prompt: { activation_id: activation, kind: 'question', text: 'Q' },
    paused_from: null,
    pending_completion: false,
    recording_active: false,
    allowed_actions: actions,
    environment: 'dev',
  };
}

const stale = () => new ApiError(409, 'stale_state_version');

describe('actionStillApplies', () => {
  it('lets Finish answer follow QUESTION_ACTIVE -> CANDIDATE_SPEAKING on the same activation', () => {
    const before = view('QUESTION_ACTIVE', 5, ['finish_answer']);
    expect(actionStillApplies('finish_answer', before, view('CANDIDATE_SPEAKING', 6, ['finish_answer']))).toBe(true);
  });

  it('refuses Finish answer once a different question is open or a completion is pending', () => {
    const before = view('QUESTION_ACTIVE', 5, ['finish_answer']);
    expect(actionStillApplies('finish_answer', before, view('QUESTION_ACTIVE', 7, ['finish_answer'], ACT2))).toBe(false);
    expect(
      actionStillApplies('finish_answer', before, { ...view('CANDIDATE_SPEAKING', 6, ['finish_answer']), pending_completion: true }),
    ).toBe(false);
  });

  it('requires the server to still allow the action', () => {
    const before = view('QUESTION_ACTIVE', 5, ['pause']);
    expect(actionStillApplies('pause', before, view('CANDIDATE_SPEAKING', 6, ['pause']))).toBe(true);
    expect(actionStillApplies('pause', before, view('PAUSED', 6, ['resume']))).toBe(false);
    expect(actionStillApplies('resume', view('PAUSED', 5, ['resume']), view('QUESTION_ACTIVE', 6, ['pause']))).toBe(false);
    expect(actionStillApplies('begin', view('CALIBRATION', 5, ['begin']), view('QUESTION_ACTIVE', 6, ['begin']))).toBe(false);
    expect(actionStillApplies('start', view('READY', 5, ['start']), view('READY', 6, ['start']))).toBe(true);
    expect(actionStillApplies('withdraw', view('PAUSED', 5, ['withdraw']), view('CANCELLED', 6, []))).toBe(false);
  });
});

describe('withStaleRetry', () => {
  it('returns the first result when the version was current', async () => {
    const perform = vi.fn(async (v: InterviewCandidateView) => v.state_version);
    const fetchLatest = vi.fn();
    await expect(withStaleRetry(view('QUESTION_ACTIVE', 5, ['pause']), perform, { action: 'pause', fetchLatest })).resolves.toBe(5);
    expect(fetchLatest).not.toHaveBeenCalled();
  });

  it('refetches and retries once with the fresh version when the intent still applies', async () => {
    const perform = vi
      .fn<(v: InterviewCandidateView) => Promise<number>>()
      .mockRejectedValueOnce(stale())
      .mockImplementation(async (v) => v.state_version);
    const fresh = view('CANDIDATE_SPEAKING', 6, ['finish_answer']);
    const onFresh = vi.fn();
    const result = await withStaleRetry(view('QUESTION_ACTIVE', 5, ['finish_answer']), perform, {
      action: 'finish_answer',
      fetchLatest: async () => fresh,
      onFresh,
    });
    expect(result).toBe(6);
    expect(perform).toHaveBeenCalledTimes(2);
    expect(onFresh).toHaveBeenCalledWith(fresh);
  });

  it('reports the conflict when the intent no longer applies', async () => {
    const perform = vi.fn(async () => {
      throw stale();
    });
    await expect(
      withStaleRetry(view('QUESTION_ACTIVE', 5, ['finish_answer']), perform, {
        action: 'finish_answer',
        fetchLatest: async () => view('QUESTION_ACTIVE', 8, ['finish_answer'], ACT2),
      }),
    ).rejects.toMatchObject({ code: 'stale_state_version' });
    expect(perform).toHaveBeenCalledTimes(1);
  });

  it('never retries other errors and retries a stale version at most once', async () => {
    const other = vi.fn(async () => {
      throw new ApiError(409, 'transition_not_allowed');
    });
    const fetchLatest = vi.fn(async () => view('QUESTION_ACTIVE', 6, ['pause']));
    await expect(withStaleRetry(view('QUESTION_ACTIVE', 5, ['pause']), other, { action: 'pause', fetchLatest })).rejects.toMatchObject({
      code: 'transition_not_allowed',
    });
    expect(fetchLatest).not.toHaveBeenCalled();

    const alwaysStale = vi.fn(async () => {
      throw stale();
    });
    await expect(
      withStaleRetry(view('QUESTION_ACTIVE', 5, ['pause']), alwaysStale, { action: 'pause', fetchLatest }),
    ).rejects.toMatchObject({ code: 'stale_state_version' });
    expect(alwaysStale).toHaveBeenCalledTimes(2);
  });

  it('surfaces the original conflict if the refetch fails', async () => {
    const perform = vi.fn(async () => {
      throw stale();
    });
    await expect(
      withStaleRetry(view('PAUSED', 5, ['resume']), perform, {
        action: 'resume',
        fetchLatest: async () => {
          throw new ApiError(0, 'network_error');
        },
      }),
    ).rejects.toMatchObject({ code: 'stale_state_version' });
  });
});
