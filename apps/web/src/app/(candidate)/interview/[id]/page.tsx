import type { Metadata } from 'next';
import { notFound } from 'next/navigation';
import { InterviewApp } from '@/features/interview/InterviewApp';
import { getMessages } from '@/i18n';
import { requestLocale } from '@/i18n/server';
import { isInterviewId } from '@/lib/api/ids';

export async function generateMetadata(): Promise<Metadata> {
  return { title: getMessages(await requestLocale()).meta.interviewTitle };
}

export default async function InterviewPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  // Malformed IDs never reach the API (the server would answer 404 anyway: no enumeration).
  if (!isInterviewId(id)) notFound();
  return <InterviewApp interviewId={id} />;
}
