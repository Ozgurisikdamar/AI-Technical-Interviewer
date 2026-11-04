'use client';

import ui from '@/components/ui.module.css';
import { useMessages } from '@/i18n/client';

/** Route-level error boundary. Never renders the error message (it may contain request data). */
export default function RouteError({ retry }: { error: Error & { digest?: string }; retry: () => void }) {
  const t = useMessages();
  return (
    <div className={ui.page} role="alert">
      <h1>{t.errorBoundary.heading}</h1>
      <p>{t.errorBoundary.body}</p>
      <div className={ui.actions}>
        <button type="button" className={`${ui.button} ${ui.primary}`} onClick={() => retry()}>
          {t.errorBoundary.retry}
        </button>
      </div>
    </div>
  );
}
