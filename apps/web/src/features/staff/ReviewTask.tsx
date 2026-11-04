'use client';

import Link from 'next/link';
import { useEffect, useRef, useState } from 'react';
import { ErrorNotice } from '@/components/ErrorNotice';
import ui from '@/components/ui.module.css';
import { useMessages } from '@/i18n/client';
import { getReviewTask, listReviewTasks } from '@/lib/api/staff';
import type { ReviewOutcomeResult, ReviewTask } from '@/lib/api/staffTypes';
import { decisionMode } from '@/lib/staff/decision';
import { locateQuotes, type Highlights } from '@/lib/staff/highlight';
import { QUEUE_PATH, queueHref, reportHref, taskHref } from '@/lib/staff/nav';
import { nextOpenTask } from '@/lib/staff/queue';
import { splitReasons } from '@/lib/staff/reasons';
import { AIDraftDisclosure } from './AIDraft';
import { DecisionForm } from './DecisionForm';
import { NotPermitted } from './Notices';
import { OutcomeView } from './Outcome';
import { Reasons } from './Reasons';
import { RequireSession } from './RequireSession';
import { RubricView } from './RubricView';
import styles from './staff.module.css';
import { useStaffSession } from './StaffSession';
import { Transcript } from './Transcript';
import { useLoad } from './useLoad';

const NO_HIGHLIGHTS: Highlights = { ranges: new Map(), status: [] };

/** One answer to review. Keyed by task, so nothing (the draft having been opened, a recorded decision) carries over. */
export function ReviewTaskView({ taskId }: { taskId: string }) {
  return <RequireSession section="review">{() => <TaskPage key={taskId} taskId={taskId} />}</RequireSession>;
}

interface Recorded {
  readonly result: ReviewOutcomeResult;
  /** The next open answer of the interview, if any. */
  readonly next: string | null;
}

function TaskPage({ taskId }: { taskId: string }) {
  const t = useMessages();
  const { ended } = useStaffSession();
  const [state, reload] = useLoad((signal) => getReviewTask(taskId, { signal }), taskId);
  const [draftViewed, setDraftViewed] = useState(false);
  const [recorded, setRecorded] = useState<Recorded | null>(null);
  const [alreadyDecided, setAlreadyDecided] = useState(false);
  const headingRef = useRef<HTMLHeadingElement | null>(null);
  const recordedRef = useRef<HTMLHeadingElement | null>(null);

  // After a navigation focus is on nothing; start keyboard and screen-reader users at the heading.
  useEffect(() => {
    if (state.status === 'ready' && (document.activeElement === null || document.activeElement === document.body)) {
      headingRef.current?.focus();
    }
  }, [state.status]);
  useEffect(() => {
    if (recorded !== null) recordedRef.current?.focus();
  }, [recorded]);

  if (state.status === 'loading') {
    return (
      <div className={styles.wide}>
        <h1>{t.staff.task.headingLoading}</h1>
        <p role="status">{t.staff.task.loading}</p>
      </div>
    );
  }
  if (state.status === 'error') {
    if (state.error.status === 403) return <NotPermitted />;
    return (
      <div className={styles.wide}>
        <h1>{state.error.status === 404 ? t.staff.notFound.heading : t.staff.task.headingLoading}</h1>
        {state.error.status === 404 ? (
          <>
            <p>{t.staff.notFound.body}</p>
            <p>
              <Link href={QUEUE_PATH}>{t.staff.notFound.queue}</Link>
            </p>
          </>
        ) : (
          <ErrorNotice error={state.error} catalog={t.staff.errors} onRetry={reload} />
        )}
      </div>
    );
  }

  const task = state.data;
  const mode = decisionMode(task);
  const reasons = splitReasons(task.reasons);
  const highlights = task.ai_evaluation !== null ? locateQuotes(task.transcript, task.ai_evaluation.evidence) : NO_HIGHLIGHTS;

  async function onRecorded(result: ReviewOutcomeResult, current: ReviewTask) {
    let next: string | null = null;
    if (result.open_review_tasks > 0) {
      try {
        const open = await listReviewTasks({ status: 'open', interviewId: current.interview_id, limit: 200 });
        const candidate = nextOpenTask(open, current.task_id);
        next = candidate !== null ? taskHref(candidate.task_id) : null;
      } catch {
        next = null; // the queue link below still leads on
      }
    }
    setRecorded({ result, next });
  }

  return (
    <div className={styles.wide}>
      <p>
        <Link href={queueHref('open', task.interview_id)}>{t.staff.task.backToQueue}</Link>
      </p>
      <h1 ref={headingRef} tabIndex={-1} data-testid="task-heading">
        {t.staff.task.heading(task.item_id)}
      </h1>
      <p className={`${styles.meta} ${styles.mono}`}>{t.staff.task.interview(task.interview_id)}</p>

      {reasons.process.length > 0 ? (
        <section aria-labelledby="task-reasons" className={styles.section}>
          <h2 id="task-reasons">{t.staff.task.reasons}</h2>
          <Reasons reasons={reasons.process} testId="task-reasons" />
        </section>
      ) : null}

      <section aria-labelledby="task-question" className={styles.section}>
        <h2 id="task-question">{t.staff.task.question}</h2>
        <p className={styles.question} lang={task.language} data-testid="task-question">
          {task.question}
        </p>
      </section>

      <Transcript segments={task.transcript} language={task.language} ranges={highlights.ranges} headingId="task-transcript" />

      {task.rubric !== null ? <RubricView rubric={task.rubric} headingId="task-rubric" /> : null}

      {recorded !== null ? (
        <section aria-labelledby="task-recorded" className={`${styles.section} ${styles.recorded}`} data-testid="recorded">
          <h2 id="task-recorded" ref={recordedRef} tabIndex={-1}>
            {t.staff.task.recorded}
          </h2>
          {recorded.next !== null ? (
            <p>
              <Link href={recorded.next} data-testid="recorded-next">
                {t.staff.task.recordedNext}
              </Link>
            </p>
          ) : recorded.result.open_review_tasks === 0 ? (
            <p>
              <Link href={reportHref(task.interview_id)} data-testid="recorded-report">
                {t.staff.task.recordedReport}
              </Link>
            </p>
          ) : null}
          <p>
            <Link href={QUEUE_PATH} data-testid="recorded-queue">
              {t.staff.task.recordedQueue}
            </Link>
          </p>
        </section>
      ) : task.outcome !== null ? (
        <>
          {alreadyDecided ? (
            <p role="status" className={ui.info} data-testid="already-decided">
              {t.staff.task.alreadyDecided}
            </p>
          ) : null}
          <OutcomeView outcome={task.outcome} headingId="task-outcome" />
        </>
      ) : (
        <section aria-labelledby="task-decision" className={styles.section}>
          <h2 id="task-decision">{t.staff.task.decision}</h2>
          {mode === 'blocked' ? (
            <p className={ui.warning} data-testid="rubric-missing">
              {t.staff.task.rubricMissing}
            </p>
          ) : (
            <DecisionForm
              task={task}
              mode={mode}
              draftViewed={draftViewed}
              onRecorded={(result) => void onRecorded(result, task)}
              onAlreadyDecided={() => {
                setAlreadyDecided(true);
                reload();
              }}
              onSessionEnded={ended}
            />
          )}
        </section>
      )}

      {task.ai_evaluation !== null ? (
        <AIDraftDisclosure
          draft={task.ai_evaluation}
          reasons={reasons.withDraft}
          quoteStatus={highlights.status}
          language={task.language}
          onOpened={() => setDraftViewed(true)}
        />
      ) : (
        <section aria-labelledby="task-no-draft" className={styles.section}>
          <h2 id="task-no-draft">{t.staff.task.draftHeading}</h2>
          <p data-testid="no-ai-draft">{t.staff.task.noDraft}</p>
          <Reasons reasons={reasons.withDraft} />
        </section>
      )}
    </div>
  );
}
