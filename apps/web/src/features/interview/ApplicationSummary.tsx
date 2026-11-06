'use client';

import { useMessages } from '@/i18n/client';
import type { InterviewCandidateView } from '@/lib/api/types';
import { STEPS, stepForScreen, type Screen } from '@/lib/interview/viewModel';
import styles from './ApplicationSummary.module.css';

/**
 * Above the notice and the setup: where the candidate is in the interview, and which application
 * it belongs to, as the sign-in or the invitation matched it (ADR-024).
 */
export function ApplicationSummary({ view, screen }: { view: InterviewCandidateView; screen: Screen }) {
  return (
    <div className={styles.summary}>
      <Stepper screen={screen} />
      <ApplicationCard view={view} />
    </div>
  );
}

/** The steps before the interview ends, the current one marked. Nothing in it takes focus. */
function Stepper({ screen }: { screen: Screen }) {
  const t = useMessages();
  const current = stepForScreen(screen);
  if (current === null) return null;
  return (
    <ol className={styles.steps} aria-label={t.steps.label} data-testid="stepper">
      {STEPS.map((step, index) => (
        <li
          key={step}
          className={`${styles.step} ${index < current ? styles.done : ''}`}
          aria-current={index === current ? 'step' : undefined}
        >
          <span className={styles.stepNumber} aria-hidden="true">
            {index < current ? '✓' : index + 1}
          </span>
          <span>{t.steps[step]}</span>
          {index < current ? <span className="visually-hidden">{` (${t.steps.done})`}</span> : null}
        </li>
      ))}
    </ol>
  );
}

/** The interview's title, in the interview's language, and the application number. */
function ApplicationCard({ view }: { view: InterviewCandidateView }) {
  const t = useMessages();
  if (view.interview_title === null && view.application_number === null) return null;
  return (
    <div className={styles.application} data-testid="application-card">
      <p className={styles.eyebrow}>{t.application.eyebrow}</p>
      {view.interview_title !== null ? (
        <p className={styles.title} lang={view.language ?? undefined} data-testid="application-title">
          {view.interview_title}
        </p>
      ) : null}
      {view.application_number !== null ? (
        <p className={styles.number} data-testid="application-number">
          {t.application.number(view.application_number)}
        </p>
      ) : null}
    </div>
  );
}
