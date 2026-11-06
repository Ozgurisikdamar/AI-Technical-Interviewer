import Link from 'next/link';
import { connection } from 'next/server';
import { SiteHeader, SiteMain } from '@/components/SiteChrome';
import ui from '@/components/ui.module.css';
import { getMessages } from '@/i18n';
import { requestLocale } from '@/i18n/server';

/**
 * Any address the app does not serve, for candidates and staff alike. It renders outside the route
 * groups' layouts, so it brings its own header and main landmark.
 */
export default async function NotFound() {
  await connection();
  const t = getMessages(await requestLocale());
  return (
    <>
      <SiteHeader brand={t.layout.brand} webEnvironment={process.env.APP_ENV} />
      <SiteMain noscript={t.layout.noscript}>
        <div className={ui.page}>
          <h1>{t.notFound.heading}</h1>
          <p>{t.notFound.body}</p>
          <p>
            <Link href="/">{t.notFound.home}</Link>
          </p>
        </div>
      </SiteMain>
    </>
  );
}
