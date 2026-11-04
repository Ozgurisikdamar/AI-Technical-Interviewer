'use client';

import { SiteHeader, SiteMain } from '@/components/SiteChrome';
import ui from '@/components/ui.module.css';
import { useMessages } from '@/i18n/client';

/**
 * Catches errors in a route group's layout, so it brings its own header and main landmark. Errors
 * inside a page are caught by the group's own boundary. Never renders the error message (it may
 * contain request data); the banner stays shown, because the web environment is unknown here.
 */
export default function RootError({ retry }: { error: Error & { digest?: string }; retry: () => void }) {
  const t = useMessages();
  return (
    <>
      <SiteHeader brand={t.layout.brand} webEnvironment={undefined} />
      <SiteMain noscript={t.layout.noscript}>
        <div className={ui.page} role="alert">
          <h1>{t.errorBoundary.heading}</h1>
          <p>{t.errorBoundary.body}</p>
          <div className={ui.actions}>
            <button type="button" className={`${ui.button} ${ui.primary}`} onClick={() => retry()}>
              {t.errorBoundary.retry}
            </button>
          </div>
        </div>
      </SiteMain>
    </>
  );
}
