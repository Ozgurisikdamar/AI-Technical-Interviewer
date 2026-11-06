import type { Metadata } from 'next';
import { ReportList } from '@/features/staff/Reports';
import { getMessages } from '@/i18n';
import { requestLocale } from '@/i18n/server';

export async function generateMetadata(): Promise<Metadata> {
  return { title: getMessages(await requestLocale()).staff.meta.reports };
}

export default function ReportsPage() {
  return <ReportList />;
}
