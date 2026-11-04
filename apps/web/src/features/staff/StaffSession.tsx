'use client';

import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';
import { ErrorCatalogProvider } from '@/components/ErrorNotice';
import { useMessages } from '@/i18n/client';
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
  /** Asks again, showing the check: after an error. */
  readonly recheck: () => void;
  /** Asks again without showing the check, so the page stays: after signing in again in another tab. */
  readonly refresh: () => void;
}

const StaffSessionContext = createContext<StaffSessionValue | null>(null);

/** The console's session, and the console's error messages for every ErrorNotice below it. */
export function StaffSessionProvider({ children }: { children: ReactNode }) {
  const t = useMessages();
  const [state, setState] = useState<SessionState>({ status: 'checking' });
  const [checks, setChecks] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    // The answer counts only while the check is still shown: a sign-in that finished first stays.
    const settle = (next: SessionState) => setState((current) => (current.status === 'checking' ? next : current));
    getStaffSession({ signal: controller.signal }).then(
      (session) => settle({ status: 'signed_in', session }),
      (error: unknown) => {
        if (isAbortError(error)) return;
        const apiError = toApiError(error);
        settle(apiError.status === 401 ? { status: 'signed_out', reason: 'none' } : { status: 'error', error: apiError });
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
  const refresh = useCallback(() => {
    // It only updates who is signed in, so a sign-out while it was asking stays signed out. Anything
    // but an answer keeps what is shown; the next request finds out about the session.
    getStaffSession().then(
      (session) => setState((current) => (current.status === 'signed_in' ? { status: 'signed_in', session } : current)),
      () => {},
    );
  }, []);
  const value = useMemo(
    () => ({ state, signedIn, ended, signedOut, recheck, refresh }),
    [state, signedIn, ended, signedOut, recheck, refresh],
  );
  return (
    <StaffSessionContext.Provider value={value}>
      <ErrorCatalogProvider value={t.staff.errors}>{children}</ErrorCatalogProvider>
    </StaffSessionContext.Provider>
  );
}

export function useStaffSession(): StaffSessionValue {
  const value = useContext(StaffSessionContext);
  if (value === null) throw new Error('useStaffSession outside StaffSessionProvider');
  return value;
}
