'use client';

import { ErrorPage } from '@/components/ErrorPage';
import { useMessages } from '@/i18n/client';

/** Errors inside a candidate page; the page's header and footer stay. */
export default function RouteError({ retry }: { error: Error & { digest?: string }; retry: () => void }) {
  return <ErrorPage messages={useMessages().errorBoundary} retry={retry} />;
}
