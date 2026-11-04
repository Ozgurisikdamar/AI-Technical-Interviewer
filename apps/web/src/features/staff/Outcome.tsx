'use client';

import { useMessages } from '@/i18n/client';
import type { ReviewOutcome } from '@/lib/api/staffTypes';
import { formatDateTime } from '@/lib/staff/time';
import { formatPoints } from '@/lib/staff/weighted';
import { ScoresTable } from './AIDraft';
import styles from './staff.module.css';

/** A recorded decision, read-only. */
export function OutcomeView({ outcome, headingId }: { outcome: ReviewOutcome; headingId: string }) {
  const t = useMessages();
  return (
    <section aria-labelledby={headingId} className={styles.section} data-testid="outcome">
      <h2 id={headingId}>{t.staff.task.outcome}</h2>
      {outcome.scores !== null ? <ScoresTable scores={outcome.scores} testId="outcome-scores" /> : <p>{t.staff.task.outcomeNoScores}</p>}
      {outcome.weighted_score !== null ? <p>{t.staff.task.outcomeWeighted(formatPoints(outcome.weighted_score))}</p> : null}
      <h3>{t.staff.task.rationale}</h3>
      <p className={styles.question}>{outcome.rationale}</p>
      <p className={styles.meta}>
        {t.staff.task.outcomeBy(outcome.reviewer_id, formatDateTime(outcome.decided_at))}{' '}
        {outcome.ai_draft_viewed ? t.staff.task.draftViewed : t.staff.task.draftNotViewed}
      </p>
    </section>
  );
}
