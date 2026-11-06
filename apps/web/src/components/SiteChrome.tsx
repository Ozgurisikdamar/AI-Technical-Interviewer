import type { ReactNode } from 'react';
import { LanguageSwitcher } from './LanguageSwitcher';
import { PreProductionBanner } from './PreProductionBanner';
import styles from './SiteChrome.module.css';

interface SiteHeaderProps {
  brand: string;
  /** APP_ENV of the web server, read on the server; undefined (unknown) keeps the banner shown. */
  webEnvironment: string | undefined;
  /** Rendered at the end of the brand bar (the review console's navigation). */
  children?: ReactNode;
}

/**
 * The page header: the pre-production banner (ADR-020) and the brand bar, as wide as the content,
 * with the language switch. It imports nothing server-only, so client error boundaries can use it.
 */
export function SiteHeader({ brand, webEnvironment, children }: SiteHeaderProps) {
  return (
    <header className={styles.header}>
      <PreProductionBanner webEnvironment={webEnvironment} />
      <div className={styles.brandBar}>
        <div className={styles.brandRow}>
          <p className={styles.brand}>
            <BrandMark />
            {brand}
          </p>
          <LanguageSwitcher />
        </div>
        {children}
      </div>
    </header>
  );
}

/**
 * The brand's mark: a microphone in a rounded square, in gold on the navy band. Decorative, and
 * drawn with presentation attributes only (no style attribute, no data: URI).
 */
function BrandMark() {
  return (
    <svg className={styles.mark} viewBox="0 0 32 32" aria-hidden="true" focusable="false">
      <rect x="1.5" y="1.5" width="29" height="29" rx="8" fill="none" stroke="#e0b54f" strokeWidth="2" />
      <rect x="12.75" y="7" width="6.5" height="11.5" rx="3.25" fill="#e0b54f" />
      <path d="M9.75 15.25a6.25 6.25 0 0 0 12.5 0" fill="none" stroke="#e0b54f" strokeWidth="2" strokeLinecap="round" />
      <path d="M16 21.5v3.75M12.75 25.25h6.5" fill="none" stroke="#e0b54f" strokeWidth="2" strokeLinecap="round" />
    </svg>
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
