import type { Metadata } from 'next';
import { notFound } from 'next/navigation';
import { ReportView } from '@/features/staff/Reports';
import { getMessages } from '@/i18n';
import { isInterviewId } from '@/lib/api/ids';

export const metadata: Metadata = {
  title: getMessages().staff.meta.report,
};

export default async function ReportPage({ params }: { params: Promise<{ interviewId: string }> }) {
  const { interviewId } = await params;
  if (!isInterviewId(interviewId)) notFound();
  return <ReportView interviewId={interviewId} />;
}
