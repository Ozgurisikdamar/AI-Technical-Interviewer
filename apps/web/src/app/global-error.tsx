'use client';

import { getMessages } from '@/i18n';
import styles from './global-error.module.css';

/**
 * Replaces the root layout when it fails. The request's language is unknown here, so the page
 * shows Turkish, the default, and then English, each marked with its language. Uses a CSS module
 * rather than the framework default, whose inline style attributes the strict CSP would block.
 */
export default function GlobalError({ retry }: { error: unknown; retry: () => void }) {
  const tr = getMessages('tr');
  const en = getMessages('en');
  return (
    <html lang="tr">
      <body className={styles.body}>
        <title>{`${tr.errorBoundary.heading} · ${en.errorBoundary.heading}`}</title>
        <main className={styles.main} role="alert">
          <h1>{tr.errorBoundary.heading}</h1>
          <p>{tr.errorBoundary.body}</p>
          <div lang="en" className={styles.second}>
            <h2>{en.errorBoundary.heading}</h2>
            <p>{en.errorBoundary.body}</p>
          </div>
          <button type="button" className={styles.button} onClick={() => retry()}>
            {tr.errorBoundary.retry} <span lang="en">· {en.errorBoundary.retry}</span>
          </button>
        </main>
      </body>
    </html>
  );
}
