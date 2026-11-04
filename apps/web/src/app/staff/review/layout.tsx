import type { ReactNode } from 'react';
import { RequireSession } from '@/features/staff/RequireSession';

/** The review queue and its answers: reviewers and administrators only. */
export default function ReviewLayout({ children }: { children: ReactNode }) {
  return <RequireSession section="review">{children}</RequireSession>;
}
