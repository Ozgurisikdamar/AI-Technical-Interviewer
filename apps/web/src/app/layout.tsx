import type { Metadata, Viewport } from 'next';
import { connection } from 'next/server';
import type { ReactNode } from 'react';
import { DEFAULT_LOCALE, getMessages } from '@/i18n';
import { MessagesProvider } from '@/i18n/client';
import './globals.css';

const t = getMessages();

export const metadata: Metadata = {
  title: { default: t.meta.appName, template: t.meta.titleTemplate },
  description: t.home.body,
  referrer: 'no-referrer',
  robots: { index: false, follow: false, nocache: true, googleBot: { index: false, follow: false } },
  formatDetection: { telephone: false, email: false, address: false },
};

export const viewport: Viewport = {
  width: 'device-width',
  initialScale: 1,
  colorScheme: 'light',
};

/**
 * The document shell shared by the candidate pages and the review console. Each route group adds
 * its own header, main landmark and footer: `(candidate)/layout.tsx` and `staff/layout.tsx`.
 */
export default async function RootLayout({ children }: { children: ReactNode }) {
  // Every page is rendered per request so it can carry this request's CSP nonce (src/proxy.ts).
  await connection();
  return (
    <html lang={DEFAULT_LOCALE}>
      <body>
        <MessagesProvider locale={DEFAULT_LOCALE}>
          <a className="skip-link" href="#main">
            {t.layout.skipToContent}
          </a>
          {children}
        </MessagesProvider>
      </body>
    </html>
  );
}
