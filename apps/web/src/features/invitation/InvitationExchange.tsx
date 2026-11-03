'use client';

import { useRouter } from 'next/navigation';
import { useState, useSyncExternalStore } from 'react';
import { ErrorNotice } from '@/components/ErrorNotice';
import ui from '@/components/ui.module.css';
import { useMessages } from '@/i18n/client';
import { toApiError, type ApiError } from '@/lib/api/errors';
import { exchangeInvitation } from '@/lib/api/interviews';
import {
  forgetInvitationToken,
  getInvitationSnapshot,
  getServerInvitationSnapshot,
  peekInvitationToken,
  subscribeInvitation,
} from '@/lib/invitation/store';

/** Errors after which the same token can never succeed: drop it from memory. */
function isDefinitive(error: ApiError): boolean {
  return error.status === 410 || error.status === 400 || error.code === 'invitation_invalid';
}

type Phase = 'idle' | 'exchanging' | 'navigating';

/**
 * Reads the invitation token from the fragment (via the in-memory store, which also strips it from
 * the address bar) and exchanges it only when the candidate clicks Continue, so link scanners and
 * prefetchers that load the page cannot consume it. The server answers with an HttpOnly cookie;
 * this code never sees a session token.
 */
export function InvitationExchange() {
  const t = useMessages();
  const router = useRouter();
  const invitation = useSyncExternalStore(subscribeInvitation, getInvitationSnapshot, getServerInvitationSnapshot);
  const [phase, setPhase] = useState<Phase>('idle');
  const [error, setError] = useState<ApiError | null>(null);

  async function onContinue() {
    const token = peekInvitationToken();
    if (token === null || phase !== 'idle') return;
    setPhase('exchanging');
    setError(null);
    try {
      const result = await exchangeInvitation(token);
      setPhase('navigating');
      forgetInvitationToken();
      router.replace(`/interview/${encodeURIComponent(result.interview_id)}`);
    } catch (caught) {
      const apiError = toApiError(caught);
      if (isDefinitive(apiError)) forgetInvitationToken();
      setError(apiError);
      setPhase('idle');
    }
  }

  if (invitation.status === 'pending' || phase === 'navigating') {
    return (
      <div className={ui.page}>
        <h1>{t.invitation.heading}</h1>
        <p role="status">{phase === 'navigating' ? t.invitation.continuing : t.invitation.checking}</p>
      </div>
    );
  }

  const problem = invitation.hasToken
    ? null
    : {
        missing: t.invitation.missing,
        malformed: t.invitation.malformed,
        query_token: t.invitation.queryToken,
        consumed: t.invitation.consumed,
      }[invitation.problem];

  return (
    <div className={ui.page}>
      <h1>{t.invitation.heading}</h1>
      {invitation.hasToken ? (
        <>
          <p className={ui.lead}>{t.invitation.intro}</p>
          <p>{t.invitation.whatNext}</p>
          <p className={ui.muted}>{t.invitation.securityNote}</p>
        </>
      ) : null}
      {error !== null ? <ErrorNotice error={error} /> : null}
      {problem !== null && error === null ? (
        <div className={ui.warning} role="alert">
          <p>{problem}</p>
        </div>
      ) : null}
      {invitation.hasToken ? (
        <div className={ui.actions}>
          <button
            type="button"
            className={`${ui.button} ${ui.primary}`}
            onClick={onContinue}
            disabled={phase !== 'idle'}
            aria-busy={phase === 'exchanging'}
            data-testid="continue"
          >
            {phase === 'exchanging' ? t.invitation.continuing : t.invitation.continue}
          </button>
        </div>
      ) : null}
    </div>
  );
}
