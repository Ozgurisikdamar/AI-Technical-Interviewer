import ui from '@/components/ui.module.css';
import { CandidateSignIn } from '@/features/signin/CandidateSignIn';
import styles from '@/features/signin/Landing.module.css';
import { getMessages } from '@/i18n';
import { requestLocale } from '@/i18n/server';

/**
 * The start page: a candidate signs in with the application number and the access code
 * (ADR-024). The invitation link (/i) remains the other way into the same interview.
 */
export default async function HomePage() {
  const t = getMessages(await requestLocale());
  return (
    <>
      <div className={styles.hero}>
        <div className={styles.heroInner}>
          <p className={styles.eyebrow}>{t.home.eyebrow}</p>
          <h1 className={styles.heading}>{t.home.heading}</h1>
          <p className={styles.body}>{t.home.body}</p>
        </div>
      </div>
      <div className={`${ui.page} ${styles.card}`}>
        <CandidateSignIn />
        <p className={styles.linkNote}>{t.home.linkNote}</p>
      </div>
    </>
  );
}
