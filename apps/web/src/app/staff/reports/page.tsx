import type { Metadata } from 'next';
import { ReportList } from '@/features/staff/Reports';
import { getMessages } from '@/i18n';

export const metadata: Metadata = {
  title: getMessages().staff.meta.reports,
};

export default function ReportsPage() {
  return <ReportList />;
}
