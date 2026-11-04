/** Review queue helpers: tasks grouped by interview, and the next answer to review. */
import type { ReviewTaskSummary } from '@/lib/api/staffTypes';

export interface InterviewGroup {
  readonly interviewId: string;
  readonly tasks: readonly ReviewTaskSummary[];
}

/** Groups tasks by interview, keeping the queue's order (oldest interview first, answers in order). */
export function groupByInterview(tasks: readonly ReviewTaskSummary[]): InterviewGroup[] {
  const groups = new Map<string, ReviewTaskSummary[]>();
  for (const task of tasks) {
    const list = groups.get(task.interview_id) ?? [];
    list.push(task);
    groups.set(task.interview_id, list);
  }
  return [...groups].map(([interviewId, list]) => ({ interviewId, tasks: list }));
}

/** The first open task of the list other than the one just decided, or null when none is left. */
export function nextOpenTask(tasks: readonly ReviewTaskSummary[], decidedTaskId: string): ReviewTaskSummary | null {
  return tasks.find((t) => t.status === 'open' && t.task_id !== decidedTaskId) ?? null;
}
