import { connection } from 'next/server';
import type { ReactNode } from 'react';
import { SiteFooter, SiteHeader, SiteMain } from '@/components/SiteChrome';
import { getMessages } from '@/i18n';

/** The candidate pages' chrome. The URLs are unchanged: a route group adds no path segment. */
export default async function CandidateLayout({ children }: { children: ReactNode }) {
  await connection();
  const t = getMessages();
  // Server-side only; the value (not the variable) reaches the page to pick the banner state.
  const webEnvironment = process.env.APP_ENV;
  return (
    <>
      <SiteHeader brand={t.layout.brand} webEnvironment={webEnvironment} />
      <SiteMain noscript={t.layout.noscript}>{children}</SiteMain>
      <SiteFooter text={t.layout.footer} />
    </>
  );
}
