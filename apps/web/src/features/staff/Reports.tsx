'use client';

import Link from 'next/link';
import ui from '@/components/ui.module.css';
import { useMessages } from '@/i18n/client';
import { getReport, listReports } from '@/lib/api/staff';
import { PAGE_LIMIT, type ReportAnswer } from '@/lib/api/staffTypes';
import { REPORTS_PATH, reportHref } from '@/lib/staff/nav';
import { splitReasons } from '@/lib/staff/reasons';
import { formatDateTime } from '@/lib/staff/time';
import { DraftBody } from './AIDraft';
import { DecisionSummary } from './DecisionSummary';
import { Loaded } from './Loaded';
import { Reasons } from './Reasons';
import styles from './staff.module.css';
import { useLoad } from './useLoad';

/** The tenant's reports, newest first. No scores and no candidate details: it is not a ranking. */
export function ReportList() {
  const t = useMessages();
  const [state, reload] = useLoad((signal) => listReports({ signal }), 'reports');
  const header = (
    <>
      <h1>{t.staff.reports.heading}</h1>
      <p>{t.staff.reports.intro}</p>
    </>
  );
  return (
    <Loaded state={state} reload={reload} header={header} loadingText={t.staff.reports.loading}>
      {(reports) =>
        reports.length === 0 ? (
          <p data-testid="reports-empty">{t.staff.reports.empty}</p>
        ) : (
          <>
            {reports.length >= PAGE_LIMIT ? (
              <p className={ui.info} data-testid="reports-truncated">
                {t.staff.reports.truncated(PAGE_LIMIT)}
              </p>
            ) : null}
            <ul className={styles.plainList} data-testid="report-list">
              {reports.map((report) => (
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
          </>
        )
      }
    </Loaded>
  );
}

/** A report: the reviewers' decisions with their evidence. The AI draft parts stay folded. */
export function ReportView({ interviewId }: { interviewId: string }) {
  const t = useMessages();
  const [state, reload] = useLoad((signal) => getReport(interviewId, { signal }), interviewId);
  const header = (
    <>
      <p>
        <Link href={REPORTS_PATH}>{t.staff.report.backToReports}</Link>
      </p>
      <h1>{t.staff.report.heading}</h1>
      <p className={`${styles.meta} ${styles.mono}`}>{t.staff.reports.interview(interviewId)}</p>
    </>
  );
  return (
    <Loaded
      state={state}
      reload={reload}
      header={header}
      loadingText={t.staff.report.loading}
      notFound={{ href: REPORTS_PATH, label: t.staff.notFound.reports }}
    >
      {(report) => (
        <>
          <section aria-labelledby="report-notice" className={ui.warning} data-testid="report-notice">
            <h2 id="report-notice">{t.staff.report.notice}</h2>
            <p>{report.notice}</p>
          </section>
          <p className={styles.meta}>
            {t.staff.report.generated(formatDateTime(report.generated_at))} · {t.staff.reports.plan(report.plan.plan_id, report.plan.version)}
            {report.plan.synthetic ? ` · ${t.staff.reports.synthetic}` : ''}
          </p>
          <ol className={styles.plainList}>
            {report.answers.map((answer, i) => (
              <li key={answer.answer_id}>
                <AnswerPart answer={answer} n={i + 1} language={report.language} />
              </li>
            ))}
          </ol>
        </>
      )}
    </Loaded>
  );
}

function AnswerPart({ answer, n, language }: { answer: ReportAnswer; n: number; language: string }) {
  const t = useMessages();
  const reasons = splitReasons(answer.review_reasons);
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
      <DecisionSummary decision={answer.reviewed} draftViewed={answer.reviewed.ai_draft_viewed} level={4} testId="report" />
      {reasons.process.length > 0 ? (
        <>
          <h4>{t.staff.report.reasons}</h4>
          <Reasons reasons={reasons.process} />
        </>
      ) : null}
      {answer.ai_evaluation !== null ? (
        <details className={styles.details}>
          <summary>{t.staff.report.aiSummary}</summary>
          <DraftBody draft={answer.ai_evaluation} reasons={reasons.withDraft} language={language} />
        </details>
      ) : (
        <p className={styles.meta}>{t.staff.report.noAi}</p>
      )}
    </article>
  );
}
