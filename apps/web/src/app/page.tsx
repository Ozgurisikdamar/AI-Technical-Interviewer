import ui from '@/components/ui.module.css';
import { getMessages } from '@/i18n';

export default function HomePage() {
  const t = getMessages();
  return (
    <div className={ui.page}>
      <h1>{t.home.heading}</h1>
      <p className={ui.lead}>{t.home.body}</p>
    </div>
  );
}
