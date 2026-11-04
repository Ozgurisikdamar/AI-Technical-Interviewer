'use client';

import { useCallback, useEffect, useState } from 'react';
import { isAbortError, toApiError, type ApiError } from '@/lib/api/errors';
import { useStaffSession } from './StaffSession';

export type Loadable<T> =
  | { readonly status: 'loading' }
  | { readonly status: 'ready'; readonly data: T }
  | { readonly status: 'error'; readonly error: ApiError };

/**
 * Loads console data and reloads it on demand. A 401 means the session ended: the whole console
 * learns it (and offers to sign in again) instead of showing an error here.
 */
export function useLoad<T>(load: (signal: AbortSignal) => Promise<T>, key: string): [Loadable<T>, () => void] {
  const { ended } = useStaffSession();
  const [state, setState] = useState<Loadable<T>>({ status: 'loading' });
  const [reloads, setReloads] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    load(controller.signal).then(
      (data) => setState({ status: 'ready', data }),
      (error: unknown) => {
        if (isAbortError(error)) return;
        const apiError = toApiError(error);
        if (apiError.status === 401) ended();
        else setState({ status: 'error', error: apiError });
      },
    );
    return () => controller.abort();
    // `key` identifies what is loaded; `load` is a new closure on every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, reloads, ended]);
  const reload = useCallback(() => {
    setState({ status: 'loading' });
    setReloads((n) => n + 1);
  }, []);
  return [state, reload];
}
