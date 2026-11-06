import ui from '@/components/ui.module.css';
import { getMessages } from '@/i18n';
import { requestLocale } from '@/i18n/server';

export default async function HomePage() {
  const t = getMessages(await requestLocale());
  return (
    <div className={ui.page}>
      <h1>{t.home.heading}</h1>
      <p className={ui.lead}>{t.home.body}</p>
    </div>
  );
}
