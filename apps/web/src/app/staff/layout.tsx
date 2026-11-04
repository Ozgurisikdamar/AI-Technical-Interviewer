import type { Metadata } from 'next';
import { connection } from 'next/server';
import type { ReactNode } from 'react';
import { SiteFooter, SiteHeader, SiteMain } from '@/components/SiteChrome';
import { StaffNav } from '@/features/staff/StaffNav';
import { StaffSessionProvider } from '@/features/staff/StaffSession';
import { getMessages } from '@/i18n';

const t = getMessages();

export const metadata: Metadata = {
  title: { default: t.staff.layout.brand, template: `%s · ${t.staff.layout.brand}` },
  description: t.staff.layout.footer,
};

/**
 * The review console (ADR-023): staff only. Its pages fetch everything in the browser through the
 * same-origin API client, with the HttpOnly session cookie; nothing about a review is rendered on
 * the server. Responses carry noindex and a Permissions-Policy without the microphone (next.config.ts).
 */
export default async function StaffLayout({ children }: { children: ReactNode }) {
  await connection();
  return (
    <StaffSessionProvider>
      <SiteHeader brand={t.staff.layout.brand} webEnvironment={process.env.APP_ENV} wide>
        <StaffNav />
      </SiteHeader>
      <SiteMain noscript={t.staff.layout.noscript}>{children}</SiteMain>
      <SiteFooter text={t.staff.layout.footer} />
    </StaffSessionProvider>
  );
}
