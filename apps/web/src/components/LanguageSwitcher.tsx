'use client';

import { useRouter } from 'next/navigation';
import { useSyncExternalStore, useTransition } from 'react';
import { LANGUAGE_NAMES, LOCALES } from '@/i18n';
import { useLocale, useMessages } from '@/i18n/client';
import { saveLocale } from '@/i18n/preference';
import styles from './SiteChrome.module.css';

const noSubscription = () => () => {};

/**
 * The language switch: one button per language, the current one pressed. Choosing a language
 * stores it and re-renders the page from the server (router.refresh()) instead of reloading it,
 * so nothing held only in memory is lost: an invitation not yet exchanged, choices not yet
 * submitted, a typed answer, unsaved scores or the live interview. Each language's name is shown
 * in that language. The buttons wait for hydration, so a click is never lost.
 */
export function LanguageSwitcher() {
  const t = useMessages();
  const locale = useLocale();
  const router = useRouter();
  const hydrated = useSyncExternalStore(noSubscription, () => true, () => false);
  const [pending, startTransition] = useTransition();

  return (
    <div role="group" aria-label={t.layout.languageLabel} className={styles.languages} data-testid="language-switch">
      {LOCALES.map((option) => (
        <button
          key={option}
          type="button"
          lang={option}
          className={styles.language}
          aria-pressed={option === locale}
          disabled={!hydrated}
          aria-busy={pending && option !== locale ? true : undefined}
          data-testid={`lang-${option}`}
          onClick={() => {
            if (option === locale) return;
            saveLocale(option);
            startTransition(() => router.refresh());
          }}
        >
          {LANGUAGE_NAMES[option]}
        </button>
      ))}
    </div>
  );
}
