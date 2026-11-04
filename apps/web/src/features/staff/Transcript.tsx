'use client';

import { useMessages } from '@/i18n/client';
import type { TranscriptExcerpt } from '@/lib/api/staffTypes';
import { displayText, splitText, type Range } from '@/lib/staff/highlight';
import { formatOffset } from '@/lib/staff/time';
import styles from './staff.module.css';

interface TranscriptProps {
  segments: readonly TranscriptExcerpt[];
  language: string;
  /** Passages the AI draft quotes. Highlighted neutrally: they show where it looked, not a score. */
  ranges: ReadonlyMap<string, readonly Range[]>;
  headingId: string;
}

export function Transcript({ segments, language, ranges, headingId }: TranscriptProps) {
  const t = useMessages();
  const anyHighlight = [...ranges.values()].some((list) => list.length > 0);
  return (
    <section aria-labelledby={headingId} className={styles.section}>
      <h2 id={headingId}>{t.staff.task.transcript}</h2>
      {segments.length === 0 ? <p>{t.staff.task.transcriptEmpty}</p> : null}
      {anyHighlight ? (
        <p className={styles.hint} data-testid="highlight-note">
          {t.staff.task.highlightNote}
        </p>
      ) : null}
      {segments.length > 0 ? (
        <ol className={styles.transcript} data-testid="transcript">
          {segments.map((segment) => (
            <li key={segment.segment_id} className={styles.segment} data-segment={segment.segment_id}>
              {/* The offset is console text; only what the candidate said is in the interview's language. */}
              <span className={styles.offset}>{t.staff.task.at(formatOffset(segment.start_ms))}</span>
              <span lang={language} data-testid="segment-text">
                {splitText(displayText(segment), ranges.get(segment.segment_id)).map((part, i) =>
                  part.highlighted ? (
                    <mark key={i} className={styles.cited} data-testid="cited">
                      {part.text}
                    </mark>
                  ) : (
                    <span key={i}>{part.text}</span>
                  ),
                )}
              </span>
            </li>
          ))}
        </ol>
      ) : null}
    </section>
  );
}
