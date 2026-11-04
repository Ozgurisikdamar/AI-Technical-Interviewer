import type { Metadata } from 'next';
import { notFound } from 'next/navigation';
import { ReviewTaskView } from '@/features/staff/ReviewTask';
import { getMessages } from '@/i18n';
import { isReviewTaskId } from '@/lib/api/ids';

export const metadata: Metadata = {
  title: getMessages().staff.meta.task,
};

export default async function ReviewTaskPage({ params }: { params: Promise<{ taskId: string }> }) {
  const { taskId } = await params;
  // Malformed identifiers never reach the API.
  if (!isReviewTaskId(taskId)) notFound();
  return <ReviewTaskView taskId={taskId} />;
}
