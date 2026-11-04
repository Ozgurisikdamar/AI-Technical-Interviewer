import type { Metadata } from 'next';
import { ReviewQueue } from '@/features/staff/ReviewQueue';
import { getMessages } from '@/i18n';
import { queueParams } from '@/lib/staff/nav';

export const metadata: Metadata = {
  title: getMessages().staff.meta.queue,
};

export default async function ReviewQueuePage({ searchParams }: { searchParams: Promise<Record<string, string | string[] | undefined>> }) {
  // Only the known filters, validated; anything else falls back to the defaults.
  const { status, interviewId } = queueParams(await searchParams);
  return <ReviewQueue status={status} interviewId={interviewId} />;
}
