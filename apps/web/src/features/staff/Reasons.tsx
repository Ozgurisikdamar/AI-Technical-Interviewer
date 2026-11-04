'use client';

import { useMessages } from '@/i18n/client';
import styles from './staff.module.css';

/** A list of review reasons in words; a code this console does not know is shown as it is. */
export function Reasons({ reasons, testId }: { reasons: readonly string[]; testId?: string }) {
  const t = useMessages();
  if (reasons.length === 0) return null;
  return (
    <ul className={styles.reasonList} data-testid={testId}>
      {reasons.map((reason) => (
        <li key={reason} data-reason={reason}>
          {t.staff.reasons[reason] ?? <span className={styles.mono}>{reason}</span>}
        </li>
      ))}
    </ul>
  );
}
