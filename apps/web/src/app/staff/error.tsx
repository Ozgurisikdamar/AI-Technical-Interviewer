'use client';

import { ErrorPage } from '@/components/ErrorPage';
import { useMessages } from '@/i18n/client';

/** Errors inside a console page; the console's header and footer stay. */
export default function StaffError({ retry }: { error: Error & { digest?: string }; retry: () => void }) {
  return <ErrorPage messages={useMessages().staff.errorBoundary} retry={retry} />;
}
