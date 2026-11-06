import { LOCALE_COOKIE, type Locale } from './index';

/** One year: the choice outlives a session, and is cleared with the browser's cookies. */
const MAX_AGE_SECONDS = 365 * 24 * 60 * 60;

/**
 * Remembers a person's language choice. This is the only place that writes `document.cookie`
 * (an ESLint rule enforces it). The cookie holds `tr` or `en` and nothing else; the server reads
 * it for the next render.
 */
export function saveLocale(locale: Locale): void {
  const secure = window.location.protocol === 'https:' ? '; Secure' : '';
  document.cookie = `${LOCALE_COOKIE}=${locale}; Path=/; Max-Age=${MAX_AGE_SECONDS}; SameSite=Lax${secure}`;
}
