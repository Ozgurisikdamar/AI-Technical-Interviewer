import type { ReactNode } from 'react';
import { PreProductionBanner } from './PreProductionBanner';
import styles from './SiteChrome.module.css';

interface SiteHeaderProps {
  brand: string;
  /** APP_ENV of the web server, read on the server; undefined (unknown) keeps the banner shown. */
  webEnvironment: string | undefined;
  /** Rendered at the end of the brand bar (the review console's navigation). */
  children?: ReactNode;
}

/** The page header: the pre-production banner (ADR-020) and the brand bar, as wide as the content. */
export function SiteHeader({ brand, webEnvironment, children }: SiteHeaderProps) {
  return (
    <header className={styles.header}>
      <PreProductionBanner webEnvironment={webEnvironment} />
      <div className={styles.brandBar}>
        <p className={styles.brand}>{brand}</p>
        {children}
      </div>
    </header>
  );
}

/** The main landmark the skip link targets, with the no-JavaScript notice. */
export function SiteMain({ noscript, children }: { noscript: string; children: ReactNode }) {
  return (
    <main id="main" tabIndex={-1} className={styles.main}>
      <noscript>
        <p className={styles.noscript}>{noscript}</p>
      </noscript>
      {children}
    </main>
  );
}

export function SiteFooter({ text }: { text: string }) {
  return (
    <footer className={styles.footer}>
      <p>{text}</p>
    </footer>
  );
}
