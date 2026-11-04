'use client';

import { useEffect, useId, useRef, useState, type FormEvent } from 'react';
import { ErrorNotice } from '@/components/ErrorNotice';
import ui from '@/components/ui.module.css';
import { useMessages } from '@/i18n/client';
import { toApiError, type ApiError } from '@/lib/api/errors';
import { submitReviewOutcome } from '@/lib/api/staff';
import { DIMENSIONS, type Dimension, type ReviewOutcomeResult, type ReviewTask } from '@/lib/api/staffTypes';
import {
  RATIONALE_MAX,
  checkDecision,
  rationaleLength,
  validScores,
  type DecisionProblems,
  type ProblemField,
  type ScoreInputs,
} from '@/lib/staff/decision';
import { formatCenti, weightPercent, weightedCenti } from '@/lib/staff/weighted';
import styles from './staff.module.css';

interface DecisionFormProps {
  task: ReviewTask;
  mode: 'scored' | 'rationale_only';
  /** Whether the reviewer has opened the AI draft so far; recorded with the decision. */
  draftViewed: boolean;
  onRecorded: (result: ReviewOutcomeResult) => void;
  onAlreadyDecided: () => void;
  onSessionEnded: () => void;
}

/**
 * The reviewer's own scores and rationale. The inputs start empty: the AI draft never prefills
 * them (ADR-023). Problems are listed in a summary that takes focus, as on the consent form.
 */
export function DecisionForm({ task, mode, draftViewed, onRecorded, onAlreadyDecided, onSessionEnded }: DecisionFormProps) {
  const t = useMessages();
  const id = useId();
  const summaryRef = useRef<HTMLDivElement | null>(null);
  const [inputs, setInputs] = useState<ScoreInputs>({});
  const [rationale, setRationale] = useState('');
  const [attempts, setAttempts] = useState(0);
  const [pending, setPending] = useState(false);
  const [failure, setFailure] = useState<ApiError | null>(null);

  const check = checkDecision(mode, inputs, rationale, draftViewed);
  const problems: DecisionProblems | null = attempts > 0 && !check.ok ? check.problems : null;
  const fields: readonly ProblemField[] = problems !== null && !check.ok ? check.fields : [];
  const centi = mode === 'scored' && task.rubric !== null ? weightedCenti(validScores(inputs), task.rubric.weights) : null;
  const rationaleIssue = problems?.rationale ?? null;

  useEffect(() => {
    if (attempts > 0) summaryRef.current?.focus();
  }, [attempts]);

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending) return;
    setFailure(null);
    if (!check.ok) {
      setAttempts((n) => n + 1);
      return;
    }
    setPending(true);
    try {
      onRecorded(await submitReviewOutcome(task.task_id, check.decision));
    } catch (error) {
      const apiError = toApiError(error);
      if (apiError.status === 401) onSessionEnded();
      else if (apiError.code === 'review_already_completed') onAlreadyDecided();
      else setFailure(apiError);
    } finally {
      setPending(false);
    }
  }

  const fieldId = (field: ProblemField) => `${id}-${field}`;
  const label = (field: ProblemField) => (field === 'rationale' ? t.staff.task.rationale : t.staff.dimensions[field]);
  const summaryText = (field: ProblemField): string => {
    if (field === 'rationale') {
      return rationaleIssue === 'too_short'
        ? t.staff.task.problem.tooShort
        : rationaleIssue === 'too_long'
          ? t.staff.task.problem.tooLong
          : t.staff.task.problem.rationaleRequired;
    }
    return problems?.scores[field] === 'invalid' ? t.staff.task.problem.invalid(label(field)) : t.staff.task.problem.required(label(field));
  };

  return (
    <form onSubmit={(e) => void onSubmit(e)} noValidate className={styles.form} data-testid="decision-form">
      <p>{mode === 'scored' ? t.staff.task.decisionIntro : t.staff.task.decisionIntroNoRubric}</p>
      {mode === 'rationale_only' ? <p className={ui.info}>{t.staff.task.noRubric}</p> : null}

      {problems !== null ? (
        <div ref={summaryRef} tabIndex={-1} role="alert" className={ui.error} aria-labelledby={`${id}-summary`} data-testid="decision-errors">
          <h3 id={`${id}-summary`}>{t.staff.task.errorSummary}</h3>
          <ul className={styles.summaryList}>
            {fields.map((field) => (
              <li key={field}>
                <button type="button" className={styles.linkButton} onClick={() => document.getElementById(fieldId(field))?.focus()}>
                  {summaryText(field)}
                </button>
              </li>
            ))}
          </ul>
        </div>
      ) : null}

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
          {t.staff.task.rationaleHint}
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
          {t.staff.task.rationaleCount(rationaleLength(rationale))}
        </p>
        {rationaleIssue !== null ? (
          <p id={`${fieldId('rationale')}-error`} className={styles.fieldError}>
            {rationaleIssue === 'too_short'
              ? t.staff.task.fieldError.tooShort
              : rationaleIssue === 'too_long'
                ? t.staff.task.fieldError.tooLong
                : t.staff.task.fieldError.rationaleRequired}
          </p>
        ) : null}
      </div>

      {mode === 'scored' ? (
        <div>
          <p className={styles.preview} data-testid="decision-preview">
            {centi !== null ? t.staff.task.preview(formatCenti(centi)) : t.staff.task.previewPending}
          </p>
          <p className={styles.hint}>{t.staff.task.previewNote}</p>
        </div>
      ) : null}

      {failure !== null ? <ErrorNotice error={failure} catalog={t.staff.errors} /> : null}

      <div className={ui.actions}>
        <button type="submit" className={`${ui.button} ${ui.primary}`} disabled={pending} aria-busy={pending} data-testid="decision-submit">
          {pending ? t.staff.task.submitting : t.staff.task.submit}
        </button>
      </div>
    </form>
  );
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
