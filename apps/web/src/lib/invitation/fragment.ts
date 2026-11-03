/**
 * Invitation links carry a one-time token in the URL fragment (`/i#token=...`), so it never
 * appears in request lines, server logs or Referer headers (ADR-016). The page reads it once,
 * removes it from the address bar immediately and keeps it only in memory until the candidate
 * clicks "Continue" (link scanners that prefetch the page never exchange it).
 */

/** base64url today (43 chars); any URL-unreserved token of the contract's 32-128 length is accepted. */
const TOKEN_PATTERN = /^[A-Za-z0-9._~-]{32,128}$/;

export interface LocationLike {
  readonly pathname: string;
  readonly search: string;
  readonly hash: string;
}

export interface HistoryLike {
  replaceState(data: unknown, unused: string, url?: string | URL | null): void;
}

/** Returns the token from a `#token=...` fragment, or null if absent, ambiguous or malformed. */
export function parseInvitationFragment(hash: string): string | null {
  const raw = hash.startsWith('#') ? hash.slice(1) : hash;
  if (raw.length === 0 || raw.length > 512) return null;
  const values = new URLSearchParams(raw).getAll('token');
  if (values.length !== 1) return null;
  const token = values[0] ?? '';
  return TOKEN_PATTERN.test(token) ? token : null;
}

/** True when a token was (wrongly) delivered in the query string, where servers can log it. */
export function hasQueryToken(search: string): boolean {
  return new URLSearchParams(search.startsWith('?') ? search.slice(1) : search).has('token');
}

/**
 * The address the page should show after capture: same path, no fragment, and no query string
 * if a token was found there.
 */
export function cleanInvitationUrl(location: LocationLike): string {
  return hasQueryToken(location.search) ? location.pathname : location.pathname + location.search;
}

/**
 * Replaces the current history entry with the clean URL. `null` state is deliberate: the Next.js
 * router only re-syncs its own copy of the URL (which still contains the fragment) when the state
 * passed in is not one of its internal entries.
 */
export function stripInvitationFromAddressBar(location: LocationLike, history: HistoryLike): void {
  history.replaceState(null, '', cleanInvitationUrl(location));
}

export type CaptureResult =
  | { readonly kind: 'token'; readonly token: string }
  | { readonly kind: 'missing' }
  | { readonly kind: 'malformed' }
  | { readonly kind: 'query_token' };

/** Reads the token and strips the fragment (and any query token) from the address bar. */
export function captureInvitation(location: LocationLike, history: HistoryLike): CaptureResult {
  const queryToken = hasQueryToken(location.search);
  const hadFragment = location.hash.length > 1;
  const token = queryToken ? null : parseInvitationFragment(location.hash);
  if (hadFragment || queryToken) {
    stripInvitationFromAddressBar(location, history);
  }
  if (queryToken) return { kind: 'query_token' };
  if (token !== null) return { kind: 'token', token };
  return hadFragment ? { kind: 'malformed' } : { kind: 'missing' };
}
