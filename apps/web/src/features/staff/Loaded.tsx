'use client';

import Link from 'next/link';
import type { ReactNode } from 'react';
import { ErrorNotice } from '@/components/ErrorNotice';
import ui from '@/components/ui.module.css';
import { useMessages } from '@/i18n/client';
import { NotPermitted } from './Notices';
import type { Loadable } from './useLoad';

interface LoadedProps<T> {
  state: Loadable<T>;
  reload: () => void;
  /** Shown above the content while it loads, fails or is ready: the page's heading and what goes with it. */
  header: ReactNode;
  loadingText: string;
  /** Where a 404 leads; without it a 404 is shown like any other error. */
  notFound?: { readonly href: string; readonly label: string };
  children: (data: T) => ReactNode;
}

/** A console page that loads its data: loading, not permitted (403), not found (404) or an error, then the content. */
export function Loaded<T>({ state, reload, header, loadingText, notFound, children }: LoadedProps<T>) {
  const t = useMessages();
  if (state.status === 'error' && state.error.status === 403) return <NotPermitted />;
  let body: ReactNode;
  if (state.status === 'loading') {
    body = <p role="status">{loadingText}</p>;
  } else if (state.status === 'ready') {
    body = children(state.data);
  } else if (state.error.status === 404 && notFound !== undefined) {
    body = (
      <>
        <p>{t.staff.notFound.body}</p>
        <p>
          <Link href={notFound.href}>{notFound.label}</Link>
        </p>
      </>
    );
  } else {
    body = <ErrorNotice error={state.error} onRetry={reload} />;
  }
  return (
    <div className={ui.page}>
      {header}
      {body}
    </div>
  );
}
