'use client';

import { createContext, useContext, useEffect, useMemo, type ReactNode } from 'react';
import { DEFAULT_LOCALE, getMessages, type Locale, type Messages } from './index';

interface LocaleValue {
  readonly locale: Locale;
  readonly messages: Messages;
}

const LocaleContext = createContext<LocaleValue>({ locale: DEFAULT_LOCALE, messages: getMessages(DEFAULT_LOCALE) });

/**
 * Provides the catalog for a locale. Catalogs hold functions, so only the locale crosses the
 * server boundary. When the language changes without a reload (router.refresh()), the document's
 * `lang` follows it.
 */
export function MessagesProvider({ locale, children }: { locale: Locale; children: ReactNode }) {
  const value = useMemo(() => ({ locale, messages: getMessages(locale) }), [locale]);
  useEffect(() => {
    document.documentElement.lang = locale;
  }, [locale]);
  return <LocaleContext.Provider value={value}>{children}</LocaleContext.Provider>;
}

export function useMessages(): Messages {
  return useContext(LocaleContext).messages;
}

export function useLocale(): Locale {
  return useContext(LocaleContext).locale;
}
