'use client';

import { useEffect, useId, useRef } from 'react';
import styles from './ErrorSummary.module.css';
import ui from './ui.module.css';

export interface ErrorSummaryItem {
  /** The id of the field the item moves focus to. */
  readonly fieldId: string;
  readonly text: string;
}

interface ErrorSummaryProps {
  heading: string;
  items: readonly ErrorSummaryItem[];
  /** Failed submits so far: the summary takes focus after each one. Nothing is shown before the first. */
  attempt: number;
  testId?: string;
}

/** The problems of a form after a failed submit, at its top. It takes focus; each item moves focus to its field. */
export function ErrorSummary({ heading, items, attempt, testId }: ErrorSummaryProps) {
  const id = useId();
  const ref = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    if (attempt > 0) ref.current?.focus();
  }, [attempt]);
  if (attempt === 0 || items.length === 0) return null;
  return (
    <div ref={ref} tabIndex={-1} role="alert" className={ui.error} aria-labelledby={`${id}-heading`} data-testid={testId}>
      <h3 id={`${id}-heading`}>{heading}</h3>
      <ul className={styles.list}>
        {items.map((item) => (
          <li key={item.fieldId}>
            <button type="button" className={styles.linkButton} onClick={() => document.getElementById(item.fieldId)?.focus()}>
              {item.text}
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}
