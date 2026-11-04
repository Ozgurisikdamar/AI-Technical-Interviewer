'use client';

import Link from 'next/link';
import { ErrorNotice } from '@/components/ErrorNotice';
import ui from '@/components/ui.module.css';
import { useMessages } from '@/i18n/client';
import { getReport, listReports } from '@/lib/api/staff';
import type { ReportAnswer } from '@/lib/api/staffTypes';
import { REPORTS_PATH, reportHref } from '@/lib/staff/nav';
import { splitReasons } from '@/lib/staff/reasons';
import { formatDateTime } from '@/lib/staff/time';
import { formatPoints } from '@/lib/staff/weighted';
import { EvidenceList, ScoresTable } from './AIDraft';
import { NotPermitted } from './Notices';
import { Reasons } from './Reasons';
import { RequireSession } from './RequireSession';
import styles from './staff.module.css';
import { useLoad } from './useLoad';

/** The tenant's reports, newest first. No scores and no candidate details: it is not a ranking. */
export function ReportList() {
  return <RequireSession section="reports">{() => <List />}</RequireSession>;
}

function List() {
  const t = useMessages();
  const [state, reload] = useLoad((signal) => listReports({ signal }), 'reports');
  if (state.status === 'error' && state.error.status === 403) return <NotPermitted />;
  return (
    <div className={styles.wide}>
      <h1>{t.staff.reports.heading}</h1>
      <p>{t.staff.reports.intro}</p>
      {state.status === 'loading' ? <p role="status">{t.staff.reports.loading}</p> : null}
      {state.status === 'error' ? <ErrorNotice error={state.error} catalog={t.staff.errors} onRetry={reload} /> : null}
      {state.status === 'ready' && state.data.length === 0 ? <p data-testid="reports-empty">{t.staff.reports.empty}</p> : null}
      {state.status === 'ready' && state.data.length > 0 ? (
        <ul className={styles.plainList} data-testid="report-list">
          {state.data.map((report) => (
            <li key={report.interview_id} className={styles.group} data-testid="report-entry" data-interview={report.interview_id}>
              <h2>
                <span className={styles.mono}>{t.staff.reports.interview(report.interview_id)}</span>
              </h2>
              <p className={styles.meta}>
                {t.staff.reports.generated(formatDateTime(report.generated_at))} · {t.staff.reports.answers(report.answer_count)} ·{' '}
                {t.staff.reports.plan(report.plan.plan_id, report.plan.version)}
                {report.plan.synthetic ? ` · ${t.staff.reports.synthetic}` : ''}
              </p>
              <p>
                <Link href={reportHref(report.interview_id)} data-testid="report-open">
                  {t.staff.reports.open(report.interview_id)}
                </Link>
              </p>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}

/** A report: the reviewers' decisions with their evidence. The AI draft parts stay folded. */
export function ReportView({ interviewId }: { interviewId: string }) {
  return <RequireSession section="reports">{() => <Report interviewId={interviewId} />}</RequireSession>;
}

function Report({ interviewId }: { interviewId: string }) {
  const t = useMessages();
  const [state, reload] = useLoad((signal) => getReport(interviewId, { signal }), interviewId);
  if (state.status === 'error' && state.error.status === 403) return <NotPermitted />;
  return (
    <div className={styles.wide}>
      <p>
        <Link href={REPORTS_PATH}>{t.staff.report.backToReports}</Link>
      </p>
      <h1>{t.staff.report.heading}</h1>
      <p className={`${styles.meta} ${styles.mono}`}>{t.staff.reports.interview(interviewId)}</p>
      {state.status === 'loading' ? <p role="status">{t.staff.report.loading}</p> : null}
      {state.status === 'error' && state.error.status === 404 ? (
        <>
          <p>{t.staff.notFound.body}</p>
          <p>
            <Link href={REPORTS_PATH}>{t.staff.notFound.reports}</Link>
          </p>
        </>
      ) : null}
      {state.status === 'error' && state.error.status !== 404 ? (
        <ErrorNotice error={state.error} catalog={t.staff.errors} onRetry={reload} />
      ) : null}
      {state.status === 'ready' ? (
        <>
          <section aria-labelledby="report-notice" className={ui.warning} data-testid="report-notice">
            <h2 id="report-notice">{t.staff.report.notice}</h2>
            <p>{state.data.notice}</p>
          </section>
          <p className={styles.meta}>
            {t.staff.report.generated(formatDateTime(state.data.generated_at))} ·{' '}
            {t.staff.reports.plan(state.data.plan.plan_id, state.data.plan.version)}
            {state.data.plan.synthetic ? ` · ${t.staff.reports.synthetic}` : ''}
          </p>
          <ol className={styles.plainList}>
            {state.data.answers.map((answer, i) => (
              <li key={answer.answer_id}>
                <AnswerPart answer={answer} n={i + 1} language={state.data.language} />
              </li>
            ))}
          </ol>
        </>
      ) : null}
    </div>
  );
}

function AnswerPart({ answer, n, language }: { answer: ReportAnswer; n: number; language: string }) {
  const t = useMessages();
  const reasons = splitReasons(answer.review_reasons);
  const viewed = answer.reviewed.ai_draft_viewed;
  return (
    <article aria-labelledby={`answer-${answer.answer_id}`} className={styles.report} data-testid="report-answer">
      <h2 id={`answer-${answer.answer_id}`}>{t.staff.report.answer(n, answer.item_id)}</h2>
      <h3>{t.staff.report.question}</h3>
      <p className={styles.question} lang={language}>
        {answer.question}
      </p>
      <p className={styles.meta}>
        {answer.rubric !== null
          ? `${t.staff.report.rubric(answer.rubric)}${answer.rubric_synthetic === true ? ` (${t.staff.report.rubricSynthetic})` : ''}`
          : t.staff.report.noRubric}
      </p>
      <h3>{t.staff.report.reviewed}</h3>
      {answer.reviewed.scores !== null ? <ScoresTable scores={answer.reviewed.scores} testId="report-scores" /> : null}
      {answer.reviewed.weighted_score !== null ? (
        <p data-testid="report-weighted">{t.staff.report.weighted(formatPoints(answer.reviewed.weighted_score))}</p>
      ) : null}
      <h4>{t.staff.report.rationale}</h4>
      <p className={styles.question}>{answer.reviewed.rationale}</p>
      <p className={styles.meta}>
        {t.staff.report.decidedBy(answer.reviewed.reviewer_id, formatDateTime(answer.reviewed.decided_at))}
        {viewed === true ? ` ${t.staff.report.draftViewed}` : viewed === false ? ` ${t.staff.report.draftNotViewed}` : ''}
      </p>
      {reasons.process.length > 0 ? (
        <>
          <h4>{t.staff.report.reasons}</h4>
          <Reasons reasons={reasons.process} />
        </>
      ) : null}
      {answer.ai_evaluation !== null ? (
        <details className={styles.details}>
          <summary>{t.staff.report.aiSummary}</summary>
          <p className={ui.warning}>{t.staff.task.draftNote}</p>
          <ScoresTable scores={answer.ai_evaluation.scores} />
          <p>{t.staff.task.draftWeighted(formatPoints(answer.ai_evaluation.weighted_score))}</p>
          <p className={styles.meta}>
            {t.staff.task.draftConfidence(answer.ai_evaluation.confidence.toFixed(2))} ·{' '}
            {t.staff.task.draftEvaluator(answer.ai_evaluation.evaluator_version)}
          </p>
          <Reasons reasons={reasons.withDraft} />
          <EvidenceList evidence={answer.ai_evaluation.evidence} language={language} />
        </details>
      ) : (
        <p className={styles.meta}>{t.staff.report.noAi}</p>
      )}
    </article>
  );
}
