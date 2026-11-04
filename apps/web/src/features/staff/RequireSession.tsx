'use client';

import type { ReactNode } from 'react';
import { ErrorNotice } from '@/components/ErrorNotice';
import ui from '@/components/ui.module.css';
import { useMessages } from '@/i18n/client';
import { sectionsFor, type StaffSection } from '@/lib/staff/nav';
import { NotPermitted, SignInRequired } from './Notices';
import { useStaffSession } from './StaffSession';

/** Renders a console section (its layout) only for a signed-in staff member whose role may open it. */
export function RequireSession({ section, children }: { section: StaffSection; children: ReactNode }) {
  const t = useMessages();
  const { state, recheck } = useStaffSession();
  switch (state.status) {
    case 'checking':
      return (
        <div className={ui.page}>
          <p role="status">{t.staff.signIn.checking}</p>
        </div>
      );
    case 'error':
      return (
        <div className={ui.page}>
          <ErrorNotice error={state.error} onRetry={recheck} />
        </div>
      );
    case 'signed_out':
      return <SignInRequired reason={state.reason} />;
    case 'signed_in':
      return sectionsFor(state.session.role).includes(section) ? children : <NotPermitted />;
  }
}
