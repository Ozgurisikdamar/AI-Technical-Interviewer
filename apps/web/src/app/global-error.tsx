'use client';

import { getMessages } from '@/i18n';
import styles from './global-error.module.css';

/**
 * Replaces the root layout when it fails. Uses a CSS module rather than the framework default,
 * whose inline style attributes the strict CSP would block.
 */
export default function GlobalError({ retry }: { error: unknown; retry: () => void }) {
  const t = getMessages();
  return (
    <html lang="en">
      <body className={styles.body}>
        <title>{t.errorBoundary.heading}</title>
        <main className={styles.main} role="alert">
          <h1>{t.errorBoundary.heading}</h1>
          <p>{t.errorBoundary.body}</p>
          <button type="button" className={styles.button} onClick={() => retry()}>
            {t.errorBoundary.retry}
          </button>
        </main>
      </body>
    </html>
  );
}
