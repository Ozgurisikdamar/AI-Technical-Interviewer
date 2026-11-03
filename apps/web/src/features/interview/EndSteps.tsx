'use client';

import ui from '@/components/ui.module.css';
import { useMessages } from '@/i18n/client';
import { Captions } from './Captions';
import styles from './EndSteps.module.css';
import { useFocusOnMount } from './useFocusOnMount';
import type { RealtimeApi } from './useRealtime';

/**
 * INTERVIEW_COMPLETED and later. States plainly that no automated hiring decision is made and that
 * human review and appeal exist. No scores or results are ever shown. If the room is still open,
 * the interviewer's closing remarks remain audible and captioned (the microphone is already off).
 */
export function CompletedStep({ focusOnMount, realtime }: { focusOnMount: boolean; realtime: RealtimeApi }) {
  const t = useMessages();
  const headingRef = useFocusOnMount<HTMLHeadingElement>(focusOnMount);
  const { snapshot } = realtime;
  const roomOpen = snapshot.connection === 'connected' || snapshot.connection === 'reconnecting';

  return (
    <section aria-labelledby="completed-heading">
      <h1 id="completed-heading" ref={headingRef} tabIndex={-1}>
        {t.completed.heading}
      </h1>
      <p className={ui.lead}>{t.completed.saved}</p>
      <h2>{t.completed.nextHeading}</h2>
      <ul className={styles.list} data-testid="completion-info">
        <li>{t.completed.noAutomatedDecision}</li>
        <li>{t.completed.humanReview}</li>
        <li>{t.completed.appeal}</li>
        <li>{t.completed.noScores}</li>
      </ul>
      {roomOpen ? (
        <>
          <p className={ui.muted}>{t.completed.closingRemarks}</p>
          {!snapshot.canPlaybackAudio ? (
            <button type="button" className={`${ui.button} ${ui.secondary}`} onClick={realtime.startAudio}>
              {t.room.enableAudio}
            </button>
          ) : null}
          <Captions captions={snapshot.captions} interim={snapshot.interimCaption} />
        </>
      ) : null}
    </section>
  );
}

/** CANCELLED: neutral, no reason given. */
export function CancelledStep({ focusOnMount }: { focusOnMount: boolean }) {
  const t = useMessages();
  const headingRef = useFocusOnMount<HTMLHeadingElement>(focusOnMount);
  return (
    <section aria-labelledby="cancelled-heading">
      <h1 id="cancelled-heading" ref={headingRef} tabIndex={-1}>
        {t.cancelled.heading}
      </h1>
      <p className={ui.lead}>{t.cancelled.body}</p>
      <p>{t.cancelled.contact}</p>
    </section>
  );
}
