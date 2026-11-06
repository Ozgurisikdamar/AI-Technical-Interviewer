'use client';

import Link from 'next/link';
import { usePathname, useRouter } from 'next/navigation';
import { useState } from 'react';
import { ErrorNotice } from '@/components/ErrorNotice';
import ui from '@/components/ui.module.css';
import { useLocale, useMessages } from '@/i18n/client';
import { toApiError, type ApiError } from '@/lib/api/errors';
import { signOut } from '@/lib/api/staff';
import { QUEUE_PATH, REPORTS_PATH, SIGN_IN_PATH, sectionsFor } from '@/lib/staff/nav';
import { formatDateTime } from '@/lib/staff/time';
import styles from './staff.module.css';
import { useStaffSession } from './StaffSession';

/** The console's navigation, by role, with who is signed in and the sign-out button. */
export function StaffNav() {
  const t = useMessages();
  const locale = useLocale();
  const { state, signedOut, ended } = useStaffSession();
  const pathname = usePathname();
  const router = useRouter();
  const [pending, setPending] = useState(false);
  const [failure, setFailure] = useState<ApiError | null>(null);
  if (state.status !== 'signed_in') return null;
  const { session } = state;
  const links = sectionsFor(session.role).map((section) =>
    section === 'review'
      ? { href: QUEUE_PATH, label: t.staff.layout.review, testId: 'staff-nav-review' }
      : { href: REPORTS_PATH, label: t.staff.layout.reports, testId: 'staff-nav-reports' },
  );

  async function onSignOut() {
    setPending(true);
    setFailure(null);
    try {
      await signOut();
      signedOut();
      router.push(SIGN_IN_PATH);
    } catch (error) {
      const apiError = toApiError(error);
      // A session that already ended is as good as signed out; any other failure leaves it valid.
      if (apiError.status === 401) {
        ended();
        router.push(SIGN_IN_PATH);
      } else {
        setFailure(apiError);
      }
    } finally {
      setPending(false);
    }
  }

  return (
    <nav aria-label={t.staff.layout.navLabel} className={styles.nav}>
      <ul className={styles.navList}>
        {links.map((link) => (
          <li key={link.href}>
            <Link
              href={link.href}
              className={styles.navLink}
              aria-current={pathname === link.href || pathname.startsWith(`${link.href}/`) ? 'page' : undefined}
              data-testid={link.testId}
            >
              {link.label}
            </Link>
          </li>
        ))}
      </ul>
      <p className={styles.who} data-testid="staff-who">
        {t.staff.layout.signedInAs(session.label, t.staff.roles[session.role])} ·{' '}
        {t.staff.layout.sessionUntil(formatDateTime(session.expires_at, locale))}
      </p>
      <button
        type="button"
        className={`${ui.button} ${ui.secondary} ${styles.signOut}`}
        onClick={() => void onSignOut()}
        disabled={pending}
        aria-busy={pending}
        data-testid="staff-sign-out"
      >
        {pending ? t.staff.layout.signingOut : t.staff.layout.signOut}
      </button>
      {failure !== null ? <ErrorNotice error={failure} /> : null}
    </nav>
  );
}
