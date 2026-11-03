/**
 * Message catalogs. Only English ships today; a Turkish catalog is added by creating `tr.ts`
 * with `export const tr: Messages = { ... }` and registering it below (the type checker then
 * enforces that every key and parameterised string exists).
 */
import { en } from './en';

export type Messages = typeof en;

export const LOCALES = ['en'] as const;
export type Locale = (typeof LOCALES)[number];
export const DEFAULT_LOCALE: Locale = 'en';

const catalogs: Readonly<Record<Locale, Messages>> = { en };

export function getMessages(locale: Locale = DEFAULT_LOCALE): Messages {
  return catalogs[locale];
}
