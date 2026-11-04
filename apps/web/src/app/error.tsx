'use client';

import { ErrorPage } from '@/components/ErrorPage';
import { SiteHeader, SiteMain } from '@/components/SiteChrome';
import { useMessages } from '@/i18n/client';

/**
 * Catches errors in a route group's layout, so it brings its own header and main landmark. Errors
 * inside a page are caught by the group's own boundary. The banner stays shown, because the web
 * environment is unknown here.
 */
export default function RootError({ retry }: { error: Error & { digest?: string }; retry: () => void }) {
  const t = useMessages();
  return (
    <>
      <SiteHeader brand={t.layout.brand} webEnvironment={undefined} />
      <SiteMain noscript={t.layout.noscript}>
        <ErrorPage messages={t.errorBoundary} retry={retry} />
      </SiteMain>
    </>
  );
}
