'use client';

import Link from 'next/link';
import { ErrorNotice } from '@/components/ErrorNotice';
import { useMessages } from '@/i18n/client';
import { listReviewTasks } from '@/lib/api/staff';
import type { ReviewStatus } from '@/lib/api/staffTypes';
import { queueHref, taskHref } from '@/lib/staff/nav';
import { groupByInterview } from '@/lib/staff/queue';
import { splitReasons } from '@/lib/staff/reasons';
import { formatDateTime } from '@/lib/staff/time';
import { NotPermitted } from './Notices';
import { Reasons } from './Reasons';
import { RequireSession } from './RequireSession';
import styles from './staff.module.css';
import { useLoad } from './useLoad';

interface QueueProps {
  status: ReviewStatus;
  interviewId: string | undefined;
}

/** The review queue: answers grouped by interview, with the process reasons only (ADR-023). */
export function ReviewQueue(props: QueueProps) {
  return <RequireSession section="review">{() => <Queue {...props} />}</RequireSession>;
}

function Queue({ status, interviewId }: QueueProps) {
  const t = useMessages();
  const [state, reload] = useLoad(
    (signal) => listReviewTasks({ status, interviewId, limit: 200 }, { signal }),
    `${status}:${interviewId ?? ''}`,
  );
  if (state.status === 'error' && state.error.status === 403) return <NotPermitted />;
  const groups = state.status === 'ready' ? groupByInterview(state.data) : [];

  return (
    <div className={styles.wide}>
      <h1>{t.staff.queue.heading}</h1>
      <p>{t.staff.queue.intro}</p>
      <nav aria-label={t.staff.queue.filterLabel}>
        <ul className={styles.filters}>
          {(['open', 'completed'] as const).map((s) => (
            <li key={s}>
              <Link
                href={queueHref(s, interviewId)}
                className={styles.filterLink}
                aria-current={s === status ? 'page' : undefined}
                data-testid={`queue-filter-${s}`}
              >
                {s === 'open' ? t.staff.queue.open : t.staff.queue.completed}
              </Link>
            </li>
          ))}
        </ul>
      </nav>
      {interviewId !== undefined ? (
        <p className={styles.meta}>
          {t.staff.queue.filteredTo(interviewId)} <Link href={queueHref(status)}>{t.staff.queue.showAll}</Link>
        </p>
      ) : null}
      {state.status === 'loading' ? <p role="status">{t.staff.queue.loading}</p> : null}
      {state.status === 'error' ? <ErrorNotice error={state.error} catalog={t.staff.errors} onRetry={reload} /> : null}
      {state.status === 'ready' && groups.length === 0 ? (
        <p data-testid="queue-empty">{status === 'open' ? t.staff.queue.empty : t.staff.queue.emptyCompleted}</p>
      ) : null}
      {groups.map((group) => (
        <section key={group.interviewId} aria-labelledby={`group-${group.interviewId}`} className={styles.group} data-testid="queue-group">
          <h2 id={`group-${group.interviewId}`}>
            <span className={styles.mono}>{t.staff.queue.interview(group.interviewId)}</span>
          </h2>
          <ol className={styles.taskList}>
            {group.tasks.map((task) => (
                <li key={task.task_id} className={styles.taskItem} data-testid="queue-task">
                  <p>
                    <strong>{t.staff.queue.answer(task.item_id)}</strong>
                  </p>
                  <p className={styles.meta}>
                    {task.has_ai_evaluation ? t.staff.queue.aiDraft : t.staff.queue.noAiDraft} ·{' '}
                    {task.completed_at !== null
                      ? t.staff.queue.reviewedAt(formatDateTime(task.completed_at))
                      : t.staff.queue.waitingSince(formatDateTime(task.created_at))}
                  </p>
                  <Reasons reasons={splitReasons(task.reasons).process} testId="queue-reasons" />
                  <p>
                    <Link href={taskHref(task.task_id)} data-testid="queue-open-task">
                      {task.status === 'open' ? t.staff.queue.review(task.item_id) : t.staff.queue.view(task.item_id)}
                    </Link>
                  </p>
                </li>
            ))}
          </ol>
        </section>
      ))}
    </div>
  );
}
