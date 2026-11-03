'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { isSessionLost, toApiError, type ApiError } from '@/lib/api/errors';
import {
  completeCalibration,
  getInterview,
  pauseInterview,
  requestAnswerCompletion,
  resumeInterview,
  startInterview,
  submitConsent,
  submitTextResponse,
  withdrawInterview,
} from '@/lib/api/interviews';
import type { ConsentDecisionRequest, InputMode, InterviewCandidateView } from '@/lib/api/types';
import { withStaleRetry, type VersionedAction } from '@/lib/interview/staleRetry';
import { isAllowed } from '@/lib/interview/viewModel';
import type { InterviewViewApi } from './useInterviewView';

export type ActionName = VersionedAction | 'consent' | 'text_response';

export type ActionResult = { readonly ok: true } | { readonly ok: false; readonly error: ApiError };

export interface ActionFailure {
  readonly action: ActionName;
  readonly error: ApiError;
}

export interface InterviewActions {
  readonly pending: ActionName | null;
  readonly failure: ActionFailure | null;
  readonly clearFailure: () => void;
  readonly submitConsent: (view: InterviewCandidateView, body: ConsentDecisionRequest) => Promise<ActionResult>;
  readonly start: (view: InterviewCandidateView, mode: InputMode) => Promise<ActionResult>;
  readonly begin: (view: InterviewCandidateView) => Promise<ActionResult>;
  readonly pause: (view: InterviewCandidateView) => Promise<ActionResult>;
  readonly resume: (view: InterviewCandidateView) => Promise<ActionResult>;
  readonly finishAnswer: (view: InterviewCandidateView) => Promise<ActionResult>;
  readonly withdraw: (view: InterviewCandidateView) => Promise<ActionResult>;
  readonly submitText: (view: InterviewCandidateView, text: string) => Promise<ActionResult>;
  /** Called when the microphone is lost mid-answer: pause with reason device_lost if allowed. */
  readonly pauseForDeviceLoss: () => void;
}

type ViewSync = Pick<InterviewViewApi, 'view' | 'applyView' | 'nextSeq' | 'refresh' | 'markSessionLost'>;

type Perform = (view: InterviewCandidateView) => Promise<InterviewCandidateView | null>;

/**
 * Candidate actions. One state-changing request at a time; each carries the state_version the
 * candidate saw. A stale version is retried once only if the action still means the same thing
 * (see staleRetry.ts); other conflicts refresh the view and are reported. A lost session switches
 * the page to the "open your invitation again" screen.
 */
export function useInterviewActions(interviewId: string, sync: ViewSync): InterviewActions {
  const { applyView, nextSeq, refresh, markSessionLost } = sync;
  const [pending, setPending] = useState<ActionName | null>(null);
  const [failure, setFailure] = useState<ActionFailure | null>(null);
  const inFlight = useRef(false);
  const latestView = useRef<InterviewCandidateView | null>(null);

  useEffect(() => {
    latestView.current = sync.view;
  }, [sync.view]);

  const fetchLatest = useCallback(async (id: string) => {
    const seq = nextSeq();
    const fresh = await getInterview(id);
    applyView(fresh, seq);
    return fresh;
  }, [applyView, nextSeq]);

  const run = useCallback(
    async (action: ActionName, view: InterviewCandidateView, perform: Perform): Promise<ActionResult> => {
      if (inFlight.current) return { ok: false, error: toApiError(null) };
      inFlight.current = true;
      setPending(action);
      setFailure(null);
      try {
        const seq = nextSeq();
        const result =
          action === 'consent' || action === 'text_response'
            ? await perform(view)
            : await withStaleRetry(view, perform, { action, fetchLatest: () => fetchLatest(view.id) });
        if (result !== null) applyView(result, seq);
        else refresh();
        return { ok: true };
      } catch (caught) {
        const error = toApiError(caught);
        if (isSessionLost(error)) {
          markSessionLost();
        } else {
          setFailure({ action, error });
          if (error.status === 409 || error.status === 0) refresh();
        }
        return { ok: false, error };
      } finally {
        inFlight.current = false;
        setPending(null);
      }
    },
    [applyView, fetchLatest, markSessionLost, nextSeq, refresh],
  );

  const pauseForDeviceLoss = useCallback(() => {
    const view = latestView.current;
    if (view === null || !isAllowed(view, 'pause')) return;
    const seq = nextSeq();
    withStaleRetry(view, (v) => pauseInterview(interviewId, v.state_version, 'device_lost'), {
      action: 'pause',
      fetchLatest: () => fetchLatest(view.id),
    }).then(
      (result) => applyView(result, seq),
      (caught: unknown) => {
        const error = toApiError(caught);
        if (isSessionLost(error)) markSessionLost();
        else refresh();
      },
    );
  }, [applyView, fetchLatest, interviewId, markSessionLost, nextSeq, refresh]);

  const clearFailure = useCallback(() => setFailure(null), []);
  const consent = useCallback(
    (view: InterviewCandidateView, body: ConsentDecisionRequest) =>
      run('consent', view, (v) => submitConsent(v.id, body)),
    [run],
  );
  const start = useCallback(
    (view: InterviewCandidateView, mode: InputMode) =>
      run('start', view, (v) => startInterview(v.id, v.state_version, mode)),
    [run],
  );
  const begin = useCallback(
    (view: InterviewCandidateView) => run('begin', view, (v) => completeCalibration(v.id, v.state_version)),
    [run],
  );
  const pause = useCallback(
    (view: InterviewCandidateView) =>
      run('pause', view, (v) => pauseInterview(v.id, v.state_version, 'candidate_request')),
    [run],
  );
  const resume = useCallback(
    (view: InterviewCandidateView) => run('resume', view, (v) => resumeInterview(v.id, v.state_version)),
    [run],
  );
  const finishAnswer = useCallback(
    (view: InterviewCandidateView) =>
      run('finish_answer', view, async (v) => {
        const activationId = v.active_prompt?.activation_id;
        if (activationId === undefined) throw new Error('No open question');
        return requestAnswerCompletion(v.id, v.state_version, activationId);
      }),
    [run],
  );
  const withdraw = useCallback(
    (view: InterviewCandidateView) => run('withdraw', view, (v) => withdrawInterview(v.id, v.state_version)),
    [run],
  );
  const submitText = useCallback(
    (view: InterviewCandidateView, text: string) =>
      run('text_response', view, async (v) => {
        const activationId = v.active_prompt?.activation_id;
        if (activationId === undefined) throw new Error('No open question');
        await submitTextResponse(v.id, activationId, text);
        // The answer moves the interview on (new state_version): load it before the controls are
        // re-enabled, so an immediate "Finish answer" carries the current version.
        try {
          return await getInterview(v.id);
        } catch {
          return null;
        }
      }),
    [run],
  );

  return {
    pending,
    failure,
    clearFailure,
    submitConsent: consent,
    start,
    begin,
    pause,
    resume,
    finishAnswer,
    withdraw,
    submitText,
    pauseForDeviceLoss,
  };
}
