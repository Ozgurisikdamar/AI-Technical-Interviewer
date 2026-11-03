import type { Metadata, Viewport } from 'next';
import { connection } from 'next/server';
import type { ReactNode } from 'react';
import { PreProductionBanner } from '@/components/PreProductionBanner';
import { DEFAULT_LOCALE, getMessages } from '@/i18n';
import { MessagesProvider } from '@/i18n/client';
import styles from './layout.module.css';
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

export default async function RootLayout({ children }: { children: ReactNode }) {
  // Every page is rendered per request so it can carry this request's CSP nonce (src/proxy.ts).
  await connection();
  // Server-side only; the value (not the variable) reaches the page to pick the banner state.
  const webEnvironment = process.env.APP_ENV;

  return (
    <html lang={DEFAULT_LOCALE}>
      <body>
        <MessagesProvider locale={DEFAULT_LOCALE}>
          <a className="skip-link" href="#main">
            {t.layout.skipToContent}
          </a>
          <header className={styles.header}>
            <PreProductionBanner webEnvironment={webEnvironment} />
            <div className={styles.brandBar}>
              <p className={styles.brand}>{t.layout.brand}</p>
            </div>
          </header>
          <main id="main" tabIndex={-1} className={styles.main}>
            <noscript>
              <p className={styles.noscript}>{t.layout.noscript}</p>
            </noscript>
            {children}
          </main>
          <footer className={styles.footer}>
            <p>{t.layout.footer}</p>
          </footer>
        </MessagesProvider>
      </body>
    </html>
  );
}
