'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { isAbortError, isSessionLost, toApiError, type ApiError } from '@/lib/api/errors';
import { getInterview } from '@/lib/api/interviews';
import type { InterviewCandidateView } from '@/lib/api/types';
import { reportApiEnvironment } from '@/lib/environment';
import { pollDelayMs, shouldPoll } from '@/lib/interview/polling';
import { isLiveState, shouldApplyView } from '@/lib/interview/viewModel';
import { usePageVisible } from './usePageVisible';

interface ViewState {
  readonly view: InterviewCandidateView | null;
  /** Issue sequence of the request that produced `view`. */
  readonly seq: number;
}

export interface InterviewViewApi {
  readonly view: InterviewCandidateView | null;
  readonly pollError: ApiError | null;
  readonly sessionLost: boolean;
  /** Poll now (e.g. after a 409 or a dropped media connection). */
  readonly refresh: () => void;
  /** Applies a view returned by an action, unless a newer one is already shown. */
  readonly applyView: (view: InterviewCandidateView, seq: number) => void;
  /** Sequence number to take just before issuing a request whose response may carry a view. */
  readonly nextSeq: () => number;
  readonly markSessionLost: () => void;
}

/**
 * Keeps the candidate view current by short-polling GET /api/v1/interviews/{id} (one request at a
 * time, chained timeouts), and merges action responses so that late poll responses never roll
 * the UI back to an older state_version.
 */
export function useInterviewView(interviewId: string): InterviewViewApi {
  const [state, setState] = useState<ViewState>({ view: null, seq: 0 });
  const [pollError, setPollError] = useState<ApiError | null>(null);
  const [sessionLost, setSessionLost] = useState(false);
  const [pollToken, setPollToken] = useState(0);
  const seqRef = useRef(0);
  const visible = usePageVisible();
  const live = state.view !== null && isLiveState(state.view.state);

  const nextSeq = useCallback(() => {
    seqRef.current += 1;
    return seqRef.current;
  }, []);

  const applyView = useCallback((view: InterviewCandidateView, seq: number) => {
    reportApiEnvironment(view.environment);
    setState((current) => (shouldApplyView(current, view, seq) ? { view, seq } : current));
  }, []);

  const refresh = useCallback(() => setPollToken((n) => n + 1), []);
  const markSessionLost = useCallback(() => setSessionLost(true), []);

  useEffect(() => {
    if (sessionLost || !shouldPoll(visible, live)) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    let failures = 0;
    let stopped = false;

    const tick = async (): Promise<void> => {
      const seq = nextSeq();
      try {
        const view = await getInterview(interviewId, { signal: controller.signal, timeoutMs: 8_000 });
        if (stopped) return;
        failures = 0;
        applyView(view, seq);
        setPollError(null);
      } catch (error) {
        if (stopped || isAbortError(error)) return;
        const apiError = toApiError(error);
        if (isSessionLost(apiError)) {
          setSessionLost(true);
          return;
        }
        failures += 1;
        setPollError(apiError);
      }
      if (!stopped) timer = setTimeout(() => void tick(), pollDelayMs(failures, visible));
    };

    timer = setTimeout(() => void tick(), 0);
    return () => {
      stopped = true;
      if (timer !== undefined) clearTimeout(timer);
      controller.abort();
    };
  }, [interviewId, visible, live, sessionLost, pollToken, applyView, nextSeq]);

  return { view: state.view, pollError, sessionLost, refresh, applyView, nextSeq, markSessionLost };
}
