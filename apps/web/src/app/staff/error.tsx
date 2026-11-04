'use client';

import ui from '@/components/ui.module.css';
import { useMessages } from '@/i18n/client';

/** Errors inside a console page; the console's header and footer stay. Never renders the error message. */
export default function StaffError({ retry }: { error: Error & { digest?: string }; retry: () => void }) {
  const t = useMessages();
  return (
    <div className={ui.page} role="alert">
      <h1>{t.staff.errorBoundary.heading}</h1>
      <p>{t.staff.errorBoundary.body}</p>
      <div className={ui.actions}>
        <button type="button" className={`${ui.button} ${ui.primary}`} onClick={() => retry()}>
          {t.staff.errorBoundary.retry}
        </button>
      </div>
    </div>
  );
}
