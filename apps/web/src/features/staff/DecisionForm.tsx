'use client';

import { useId, useState, type FormEvent } from 'react';
import { ErrorNotice } from '@/components/ErrorNotice';
import { ErrorSummary } from '@/components/ErrorSummary';
import ui from '@/components/ui.module.css';
import { useLocale, useMessages } from '@/i18n/client';
import { toApiError, type ApiError } from '@/lib/api/errors';
import { submitReviewOutcome } from '@/lib/api/staff';
import { DIMENSIONS, type Dimension, type ReviewOutcomeResult, type ReviewTask } from '@/lib/api/staffTypes';
import {
  RATIONALE_MAX,
  RATIONALE_MIN,
  checkDecision,
  rationaleLength,
  type ProblemField,
  type RationaleProblem,
  type ScoreInputs,
} from '@/lib/staff/decision';
import { SIGN_IN_PATH } from '@/lib/staff/nav';
import { formatPoints, weightPercent, weightedCenti } from '@/lib/staff/weighted';
import styles from './staff.module.css';
import { useStaffSession } from './StaffSession';

interface DecisionFormProps {
  task: ReviewTask;
  mode: 'scored' | 'rationale_only';
  /** Whether the reviewer has opened the AI draft so far; recorded with the decision. */
  draftViewed: boolean;
  /** Runs once the decision is recorded; the form stays busy until it is done. */
  onRecorded: (result: ReviewOutcomeResult) => Promise<void>;
  onAlreadyDecided: () => void;
}

/**
 * The reviewer's own scores and rationale. The inputs start empty: the AI draft never prefills
 * them (ADR-023). Problems are listed in a summary that takes focus, as on the consent form. If
 * the session ends meanwhile, what was typed stays: the reviewer signs in again in another tab
 * (the session cookie is shared) and records the decision here.
 */
export function DecisionForm({ task, mode, draftViewed, onRecorded, onAlreadyDecided }: DecisionFormProps) {
  const t = useMessages();
  const locale = useLocale();
  const { refresh } = useStaffSession();
  const id = useId();
  const [inputs, setInputs] = useState<ScoreInputs>({});
  const [rationale, setRationale] = useState('');
  const [attempts, setAttempts] = useState(0);
  const [pending, setPending] = useState(false);
  const [failure, setFailure] = useState<ApiError | null>(null);
  const [sessionEnded, setSessionEnded] = useState(false);

  const check = checkDecision(mode, inputs, rationale, draftViewed);
  const problems = attempts > 0 && !check.ok ? check.problems : null;
  const fields: readonly ProblemField[] = attempts > 0 && !check.ok ? check.fields : [];
  const centi = mode === 'scored' && task.rubric !== null ? weightedCenti(check.values, task.rubric.weights) : null;
  const rationaleIssue = problems?.rationale ?? null;

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending) return;
    setFailure(null);
    if (!check.ok) {
      setAttempts((n) => n + 1);
      return;
    }
    const signedInAgain = sessionEnded;
    setSessionEnded(false);
    setPending(true);
    try {
      const result = await submitReviewOutcome(task.task_id, check.decision);
      // Whoever signed in again in the other tab recorded this: the navigation shows them.
      if (signedInAgain) refresh();
      await onRecorded(result);
    } catch (error) {
      const apiError = toApiError(error);
      if (apiError.status === 401) setSessionEnded(true);
      else if (apiError.code === 'review_already_completed') onAlreadyDecided();
      else setFailure(apiError);
    } finally {
      setPending(false);
    }
  }

  const fieldId = (field: ProblemField) => `${id}-${field}`;
  const label = (field: ProblemField) => (field === 'rationale' ? t.staff.task.rationale : t.staff.dimensions[field]);
  const summaryText = (field: ProblemField): string =>
    field === 'rationale'
      ? rationaleText(t.staff.task.problem, rationaleIssue)
      : problems?.scores[field] === 'invalid'
        ? t.staff.task.problem.invalid(label(field))
        : t.staff.task.problem.required(label(field));

  return (
    <form onSubmit={(e) => void onSubmit(e)} noValidate className={styles.form} data-testid="decision-form">
      <p>{mode === 'scored' ? t.staff.task.decisionIntro : t.staff.task.decisionIntroNoRubric}</p>
      {mode === 'rationale_only' ? <p className={ui.info}>{t.staff.task.noRubric}</p> : null}

      <ErrorSummary
        heading={t.staff.task.errorSummary}
        attempt={attempts}
        items={fields.map((field) => ({ fieldId: fieldId(field), text: summaryText(field) }))}
        testId="decision-errors"
      />

      {mode === 'scored' && task.rubric !== null ? (
        <fieldset className={styles.scores}>
          <legend className={styles.legend}>{t.staff.task.scores}</legend>
          {DIMENSIONS.map((dimension) => (
            <ScoreField
              key={dimension}
              id={fieldId(dimension)}
              dimension={dimension}
              weight={weightPercent(task.rubric?.weights[dimension] ?? 0)}
              value={inputs[dimension] ?? ''}
              problem={problems?.scores[dimension]}
              disabled={pending}
              onChange={(value) => setInputs((current) => ({ ...current, [dimension]: value }))}
            />
          ))}
        </fieldset>
      ) : null}

      <div className={styles.field}>
        <label htmlFor={fieldId('rationale')} className={styles.label}>
          {t.staff.task.rationale}
        </label>
        <p id={`${fieldId('rationale')}-hint`} className={styles.hint}>
          {t.staff.task.rationaleHint(RATIONALE_MIN, RATIONALE_MAX)}
        </p>
        <textarea
          id={fieldId('rationale')}
          className={styles.textarea}
          value={rationale}
          onChange={(e) => setRationale(e.target.value)}
          // A generous cap on pastes; the check counts characters (code points) as the server does.
          maxLength={RATIONALE_MAX * 2}
          disabled={pending}
          aria-invalid={rationaleIssue !== null ? true : undefined}
          aria-describedby={[
            `${fieldId('rationale')}-hint`,
            `${fieldId('rationale')}-count`,
            rationaleIssue !== null ? `${fieldId('rationale')}-error` : null,
          ]
            .filter(Boolean)
            .join(' ')}
          data-testid="decision-rationale"
        />
        <p id={`${fieldId('rationale')}-count`} className={styles.hint}>
          {t.staff.task.rationaleCount(rationaleLength(rationale), RATIONALE_MAX)}
        </p>
        {rationaleIssue !== null ? (
          <p id={`${fieldId('rationale')}-error`} className={styles.fieldError}>
            {rationaleText(t.staff.task.fieldError, rationaleIssue)}
          </p>
        ) : null}
      </div>

      {mode === 'scored' ? (
        <div>
          <p className={styles.preview} data-testid="decision-preview">
            {centi !== null ? t.staff.task.preview(formatPoints(centi / 100, locale)) : t.staff.task.previewPending}
          </p>
          <p className={styles.hint}>{t.staff.task.previewNote}</p>
        </div>
      ) : null}

      {sessionEnded ? (
        <div role="alert" className={ui.warning} data-testid="decision-session-ended">
          <p>{t.staff.task.sessionEnded}</p>
          <p>
            {/* No return address: the answer is not opened a second time there. */}
            <a href={SIGN_IN_PATH} target="_blank" rel="noopener" data-testid="decision-sign-in">
              {t.staff.task.signInNewTab}
              <span className="visually-hidden"> {t.staff.task.opensInNewTab}</span>
            </a>
          </p>
        </div>
      ) : null}
      {failure !== null ? <ErrorNotice error={failure} /> : null}

      <div className={ui.actions}>
        <button type="submit" className={`${ui.button} ${ui.primary}`} disabled={pending} aria-busy={pending} data-testid="decision-submit">
          {pending ? t.staff.task.submitting : t.staff.task.submit}
        </button>
      </div>
    </form>
  );
}

/** The rationale's problem in the summary's words or the field's. */
function rationaleText(
  messages: { rationaleRequired: string; tooShort: (min: number) => string; tooLong: (max: number) => string },
  issue: RationaleProblem | null,
): string {
  if (issue === 'too_short') return messages.tooShort(RATIONALE_MIN);
  if (issue === 'too_long') return messages.tooLong(RATIONALE_MAX);
  return messages.rationaleRequired;
}

interface ScoreFieldProps {
  id: string;
  dimension: Dimension;
  weight: number;
  value: string;
  problem: 'required' | 'invalid' | undefined;
  disabled: boolean;
  onChange: (value: string) => void;
}

function ScoreField({ id, dimension, weight, value, problem, disabled, onChange }: ScoreFieldProps) {
  const t = useMessages();
  return (
    <div className={styles.field}>
      <label htmlFor={id} className={styles.label}>
        {t.staff.dimensions[dimension]} <span className={styles.scoreWeight}>{t.staff.task.weight(weight)}</span>
      </label>
      <p id={`${id}-hint`} className={styles.hint}>
        {t.staff.task.scoreHint}
      </p>
      <input
        id={id}
        className={styles.input}
        type="text"
        inputMode="numeric"
        autoComplete="off"
        maxLength={3}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        aria-invalid={problem !== undefined ? true : undefined}
        aria-describedby={problem !== undefined ? `${id}-hint ${id}-error` : `${id}-hint`}
        data-testid={`score-${dimension}`}
      />
      {problem !== undefined ? (
        <p id={`${id}-error`} className={styles.fieldError}>
          {problem === 'invalid' ? t.staff.task.fieldError.invalid : t.staff.task.fieldError.required}
        </p>
      ) : null}
    </div>
  );
}
