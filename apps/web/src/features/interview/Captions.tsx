'use client';

import { useEffect, useRef } from 'react';
import { useMessages } from '@/i18n/client';
import type { CaptionLine } from '@/lib/realtime/captions';
import styles from './Captions.module.css';

interface CaptionsProps {
  captions: readonly CaptionLine[];
  interim: CaptionLine | null;
}

/**
 * Interviewer captions. Finished lines are appended to a polite live region, so screen readers
 * announce each completed sentence once; the line still being spoken is shown visually only.
 */
export function Captions({ captions, interim }: CaptionsProps) {
  const t = useMessages();
  const boxRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const box = boxRef.current;
    if (box) box.scrollTop = box.scrollHeight;
  }, [captions, interim]);

  return (
    <section aria-labelledby="captions-heading" className={styles.section}>
      <h2 id="captions-heading" className={styles.heading}>
        {t.room.captionsHeading}
      </h2>
      <div ref={boxRef} className={styles.box} role="region" aria-labelledby="captions-heading" tabIndex={0}>
        {captions.length === 0 && interim === null ? <p className={styles.empty}>{t.room.captionsEmpty}</p> : null}
        <div role="log" aria-live="polite" aria-relevant="additions text" data-testid="captions">
          {captions.map((line) => (
            <p key={line.id} className={styles.line}>
              {line.text}
            </p>
          ))}
        </div>
        {interim !== null ? (
          <p className={`${styles.line} ${styles.interim}`} aria-hidden="true">
            {interim.text}
          </p>
        ) : null}
      </div>
    </section>
  );
}
