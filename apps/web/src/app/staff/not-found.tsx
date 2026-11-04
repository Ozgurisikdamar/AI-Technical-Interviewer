import Link from 'next/link';
import ui from '@/components/ui.module.css';
import { getMessages } from '@/i18n';
import { QUEUE_PATH, REPORTS_PATH } from '@/lib/staff/nav';

/** A malformed task or interview identifier in a console address (notFound() in the pages). */
export default function StaffNotFound() {
  const t = getMessages();
  return (
    <div className={ui.page}>
      <h1>{t.staff.notFound.heading}</h1>
      <p>{t.staff.notFound.body}</p>
      <p>
        <Link href={QUEUE_PATH}>{t.staff.notFound.queue}</Link> · <Link href={REPORTS_PATH}>{t.staff.notFound.reports}</Link>
      </p>
    </div>
  );
}
