import { intlLocale, LOCALES, type Locale } from '@/i18n';

/**
 * Times in the review console are shown in UTC, labelled as such, so server and client render
 * the same text and reviewers in different time zones read the same value. The month is a short
 * name, never a number, so a date cannot be read in the wrong order.
 */
const DATE_TIME = Object.fromEntries(
  LOCALES.map((locale) => [
    locale,
    new Intl.DateTimeFormat(intlLocale(locale), {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
      hourCycle: 'h23',
      timeZone: 'UTC',
    }),
  ]),
) as Record<Locale, Intl.DateTimeFormat>;

/** "4 Nov 2026, 13:13 UTC" or "4 Kas 2026 13:13 UTC", or "—" for a value that is not a time. */
export function formatDateTime(iso: string, locale: Locale): string {
  const at = Date.parse(iso);
  return Number.isNaN(at) ? '—' : `${DATE_TIME[locale].format(at)} UTC`;
}

/** A transcript offset: 83_500 ms → "1:23". */
export function formatOffset(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000));
  const minutes = Math.floor(total / 60);
  const seconds = total % 60;
  return `${minutes}:${seconds.toString().padStart(2, '0')}`;
}
