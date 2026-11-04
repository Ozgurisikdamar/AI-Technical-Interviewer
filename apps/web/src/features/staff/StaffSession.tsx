'use client';

import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';
import { isAbortError, toApiError, type ApiError } from '@/lib/api/errors';
import { getStaffSession } from '@/lib/api/staff';
import type { StaffSession } from '@/lib/api/staffTypes';

/**
 * Who is signed in to the review console. The session itself is an HttpOnly cookie this code
 * never sees (ADR-023); the console only asks the server who it belongs to, once per page load,
 * and learns from a 401 that it has ended.
 */
export type SessionState =
  | { readonly status: 'checking' }
  | { readonly status: 'signed_out'; readonly reason: SignedOutReason }
  | { readonly status: 'signed_in'; readonly session: StaffSession }
  | { readonly status: 'error'; readonly error: ApiError };

/** none: never signed in here; ended: expired or revoked; signed_out: the staff member signed out. */
export type SignedOutReason = 'none' | 'ended' | 'signed_out';

interface StaffSessionValue {
  readonly state: SessionState;
  readonly signedIn: (session: StaffSession) => void;
  /** A request was answered 401: the session expired or was revoked. */
  readonly ended: () => void;
  readonly signedOut: () => void;
  readonly recheck: () => void;
}

const StaffSessionContext = createContext<StaffSessionValue | null>(null);

export function StaffSessionProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<SessionState>({ status: 'checking' });
  const [checks, setChecks] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    getStaffSession({ signal: controller.signal }).then(
      (session) => setState({ status: 'signed_in', session }),
      (error: unknown) => {
        if (isAbortError(error)) return;
        const apiError = toApiError(error);
        setState(apiError.status === 401 ? { status: 'signed_out', reason: 'none' } : { status: 'error', error: apiError });
      },
    );
    return () => controller.abort();
  }, [checks]);

  const signedIn = useCallback((session: StaffSession) => setState({ status: 'signed_in', session }), []);
  const ended = useCallback(() => setState({ status: 'signed_out', reason: 'ended' }), []);
  const signedOut = useCallback(() => setState({ status: 'signed_out', reason: 'signed_out' }), []);
  const recheck = useCallback(() => {
    setState({ status: 'checking' });
    setChecks((n) => n + 1);
  }, []);
  const value = useMemo(() => ({ state, signedIn, ended, signedOut, recheck }), [state, signedIn, ended, signedOut, recheck]);
  return <StaffSessionContext.Provider value={value}>{children}</StaffSessionContext.Provider>;
}

export function useStaffSession(): StaffSessionValue {
  const value = useContext(StaffSessionContext);
  if (value === null) throw new Error('useStaffSession outside StaffSessionProvider');
  return value;
}
