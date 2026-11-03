import { describe, expect, it, vi } from 'vitest';
import {
  captureInvitation,
  cleanInvitationUrl,
  hasQueryToken,
  parseInvitationFragment,
  stripInvitationFromAddressBar,
  type HistoryLike,
  type LocationLike,
} from './fragment';

// A base64url token like the control plane issues (32 random bytes -> 43 characters).
const TOKEN = 'test-only-QbZ3k9vX2mN8pL4rT6wY1uE5oI7aS0dF';

function fakeHistory(): HistoryLike & { calls: unknown[][] } {
  const calls: unknown[][] = [];
  return {
    calls,
    replaceState: vi.fn((...args: unknown[]) => {
      calls.push(args);
    }),
  };
}

const at = (hash: string, search = '', pathname = '/i'): LocationLike => ({ pathname, search, hash });

describe('parseInvitationFragment', () => {
  it('reads the token from #token=…', () => {
    expect(parseInvitationFragment(`#token=${TOKEN}`)).toBe(TOKEN);
    expect(parseInvitationFragment(`token=${TOKEN}`)).toBe(TOKEN);
  });

  it('ignores other fragment parameters', () => {
    expect(parseInvitationFragment(`#lang=en&token=${TOKEN}`)).toBe(TOKEN);
  });

  it('decodes percent-encoding', () => {
    expect(parseInvitationFragment(`#token=${TOKEN.replace('-', '%2D')}`)).toBe(TOKEN);
  });

  it.each([
    ['empty', ''],
    ['bare hash', '#'],
    ['no token key', `#t=${TOKEN}`],
    ['too short', '#token=abc123'],
    ['too long', `#token=${'a'.repeat(129)}`],
    ['bad characters', `#token=${TOKEN.slice(0, 40)}<script>`],
    ['spaces', `#token=${TOKEN.slice(0, 20)}%20${TOKEN.slice(20)}`],
    ['ambiguous (two tokens)', `#token=${TOKEN}&token=${TOKEN}`],
  ])('returns null when %s', (_label, hash) => {
    expect(parseInvitationFragment(hash)).toBeNull();
  });
});

describe('stripping the token from the address bar', () => {
  it('keeps path and query but drops the fragment', () => {
    expect(cleanInvitationUrl(at(`#token=${TOKEN}`, '?utm=x'))).toBe('/i?utm=x');
  });

  it('drops the whole query if a token was (wrongly) put there', () => {
    expect(hasQueryToken(`?token=${TOKEN}`)).toBe(true);
    expect(cleanInvitationUrl(at('', `?token=${TOKEN}&a=1`))).toBe('/i');
  });

  it('replaces the history entry with null state so the Next.js router re-syncs its URL', () => {
    const history = fakeHistory();
    stripInvitationFromAddressBar(at(`#token=${TOKEN}`), history);
    expect(history.calls).toEqual([[null, '', '/i']]);
  });
});

describe('captureInvitation', () => {
  it('returns the token and immediately strips it, before any request', () => {
    const history = fakeHistory();
    const fetchSpy = vi.fn();
    vi.stubGlobal('fetch', fetchSpy);

    const result = captureInvitation(at(`#token=${TOKEN}`), history);

    expect(result).toEqual({ kind: 'token', token: TOKEN });
    expect(history.calls).toHaveLength(1);
    const newUrl = history.calls[0]?.[2];
    expect(newUrl).toBe('/i');
    expect(String(newUrl)).not.toContain(TOKEN);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it('strips a malformed fragment too', () => {
    const history = fakeHistory();
    expect(captureInvitation(at('#token=short'), history)).toEqual({ kind: 'malformed' });
    expect(history.calls).toHaveLength(1);
  });

  it('refuses a token delivered in the query string and strips it', () => {
    const history = fakeHistory();
    expect(captureInvitation(at(`#token=${TOKEN}`, `?token=${TOKEN}`), history)).toEqual({ kind: 'query_token' });
    expect(history.calls[0]?.[2]).toBe('/i');
  });

  it('does not touch history when there is nothing to strip', () => {
    const history = fakeHistory();
    expect(captureInvitation(at(''), history)).toEqual({ kind: 'missing' });
    expect(history.calls).toHaveLength(0);
  });
});
