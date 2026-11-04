'use client';

import Link from 'next/link';
import ui from '@/components/ui.module.css';
import { useMessages } from '@/i18n/client';
import { REPORTS_PATH, SIGN_IN_PATH } from '@/lib/staff/nav';
import type { SignedOutReason } from './StaffSession';

/** Shown in place of a page when nobody is signed in, or the session has ended. */
export function SignInRequired({ reason }: { reason: SignedOutReason }) {
  const t = useMessages();
  const ended = reason === 'ended';
  return (
    <div className={ui.page} data-testid="staff-sign-in-required">
      <h1>{ended ? t.staff.sessionEnded.heading : t.staff.signInRequired.heading}</h1>
      <p>{ended ? t.staff.sessionEnded.body : t.staff.signInRequired.body}</p>
      <p>
        <Link href={SIGN_IN_PATH} className={`${ui.button} ${ui.primary}`}>
          {t.staff.sessionEnded.signIn}
        </Link>
      </p>
    </div>
  );
}

/** Shown when the signed-in role may not open the page (the server refuses it as well). */
export function NotPermitted() {
  const t = useMessages();
  return (
    <div className={ui.page} data-testid="staff-not-permitted">
      <h1>{t.staff.notPermitted.heading}</h1>
      <p>{t.staff.notPermitted.body}</p>
      <p>
        <Link href={REPORTS_PATH}>{t.staff.notPermitted.reports}</Link>
      </p>
    </div>
  );
}
