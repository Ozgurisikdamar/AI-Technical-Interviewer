import type { Metadata } from 'next';
import { notFound } from 'next/navigation';
import { TaskPage } from '@/features/staff/ReviewTask';
import { getMessages } from '@/i18n';
import { isReviewTaskId } from '@/lib/api/ids';

export const metadata: Metadata = {
  title: getMessages().staff.meta.task,
};

export default async function ReviewTaskPage({ params }: { params: Promise<{ taskId: string }> }) {
  const { taskId } = await params;
  // Malformed identifiers never reach the API.
  if (!isReviewTaskId(taskId)) notFound();
  // Keyed by task: moving to the next answer starts a fresh page.
  return <TaskPage key={taskId} taskId={taskId} />;
}
