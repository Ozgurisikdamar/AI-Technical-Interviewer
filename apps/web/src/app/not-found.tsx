import Link from 'next/link';
import ui from '@/components/ui.module.css';
import { getMessages } from '@/i18n';

export default function NotFound() {
  const t = getMessages();
  return (
    <div className={ui.page}>
      <h1>{t.notFound.heading}</h1>
      <p>{t.notFound.body}</p>
      <p>
        <Link href="/">{t.notFound.home}</Link>
      </p>
    </div>
  );
}
