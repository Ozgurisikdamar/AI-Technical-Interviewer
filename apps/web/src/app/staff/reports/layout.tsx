import type { ReactNode } from 'react';
import { RequireSession } from '@/features/staff/RequireSession';

/** The reports: every staff role. */
export default function ReportsLayout({ children }: { children: ReactNode }) {
  return <RequireSession section="reports">{children}</RequireSession>;
}
