'use client';

import { useCallback, useEffect, useEffectEvent, useState } from 'react';
import { isAbortError, toApiError, type ApiError } from '@/lib/api/errors';
import { useStaffSession } from './StaffSession';

export type Loadable<T> =
  | { readonly status: 'loading' }
  | { readonly status: 'ready'; readonly data: T }
  | { readonly status: 'error'; readonly error: ApiError };

const LOADING = { status: 'loading' } as const;

interface Result<T> {
  readonly key: string;
  readonly attempt: number;
  readonly value: Loadable<T>;
}

/**
 * Loads console data for `key` and reloads it on demand. What was loaded for one key (or before a
 * reload) is never shown for another: until the new answer arrives the state is 'loading'. A 401
 * means the session ended: the whole console learns it (and offers to sign in again) instead of
 * showing an error here.
 */
export function useLoad<T>(load: (signal: AbortSignal) => Promise<T>, key: string): [Loadable<T>, () => void] {
  const { ended } = useStaffSession();
  const [result, setResult] = useState<Result<T> | null>(null);
  const [attempt, setAttempt] = useState(0);
  // `load` is a new closure on every render; `key` says what it loads.
  const start = useEffectEvent((signal: AbortSignal) => load(signal));

  useEffect(() => {
    const controller = new AbortController();
    const { signal } = controller;
    start(signal).then(
      (data) => {
        if (!signal.aborted) setResult({ key, attempt, value: { status: 'ready', data } });
      },
      (error: unknown) => {
        if (signal.aborted || isAbortError(error)) return;
        const apiError = toApiError(error);
        if (apiError.status === 401) ended();
        else setResult({ key, attempt, value: { status: 'error', error: apiError } });
      },
    );
    return () => controller.abort();
  }, [key, attempt, ended]);

  const reload = useCallback(() => setAttempt((n) => n + 1), []);
  const current = result !== null && result.key === key && result.attempt === attempt ? result.value : LOADING;
  return [current, reload];
}
