'use client';

import { useLocale, useMessages } from '@/i18n/client';
import type { RecordedDecision } from '@/lib/api/staffTypes';
import { formatDateTime } from '@/lib/staff/time';
import { formatPoints } from '@/lib/staff/weighted';
import { ScoresTable } from './AIDraft';
import styles from './staff.module.css';

interface DecisionSummaryProps {
  decision: RecordedDecision;
  /** Whether the AI draft was opened before deciding; null in reports written before the console recorded it. */
  draftViewed: boolean | null;
  /** The level of the rationale's heading: one below the section's own. */
  level: 3 | 4;
  /** Prefix of the scores' and weighted score's test ids. */
  testId: string;
}

/** A recorded decision, read-only: on its task once decided, and in the report. */
export function DecisionSummary({ decision, draftViewed, level, testId }: DecisionSummaryProps) {
  const t = useMessages();
  const locale = useLocale();
  const Heading = level === 3 ? 'h3' : 'h4';
  return (
    <>
      {decision.scores !== null ? (
        <ScoresTable scores={decision.scores} testId={`${testId}-scores`} />
      ) : (
        <p>{t.staff.decision.noScores}</p>
      )}
      {decision.weighted_score !== null ? (
        <p data-testid={`${testId}-weighted`}>{t.staff.decision.weighted(formatPoints(decision.weighted_score, locale))}</p>
      ) : null}
      <Heading>{t.staff.decision.rationale}</Heading>
      <p className={styles.question}>{decision.rationale}</p>
      <p className={styles.meta}>
        {t.staff.decision.by(decision.reviewer_id, formatDateTime(decision.decided_at, locale))}
        {draftViewed === null ? '' : ` ${draftViewed ? t.staff.decision.draftViewed : t.staff.decision.draftNotViewed}`}
      </p>
    </>
  );
}
