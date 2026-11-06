/**
 * Message catalogs. Turkish is the default; English is the alternative a person can pick. The
 * choice is kept in the `ati_lang` cookie, written only when someone picks a language
 * (./preference.ts) and read on the server for every request (./server.ts). `Accept-Language`
 * is not consulted. `tr.ts` is typed as `Messages`, so the type checker enforces that both
 * catalogs hold every key and every parameterised string.
 */
import { en } from './en';
import { tr } from './tr';

export type Messages = typeof en;

export const LOCALES = ['tr', 'en'] as const;
export type Locale = (typeof LOCALES)[number];
export const DEFAULT_LOCALE: Locale = 'tr';

/** The cookie that holds a person's language choice. */
export const LOCALE_COOKIE = 'ati_lang';

/** Each language's name in that language: the switch shows them this way in both catalogs. */
export const LANGUAGE_NAMES: Readonly<Record<Locale, string>> = { tr: 'Türkçe', en: 'English' };

export function isLocale(value: unknown): value is Locale {
  return value === 'tr' || value === 'en';
}

/** The locale passed to Intl formatters. */
export function intlLocale(locale: Locale): string {
  return locale === 'tr' ? 'tr-TR' : 'en-GB';
}

const catalogs: Readonly<Record<Locale, Messages>> = { tr, en };

/** The catalog of a locale. There is no default argument, so every caller states the locale. */
export function getMessages(locale: Locale): Messages {
  return catalogs[locale];
}
