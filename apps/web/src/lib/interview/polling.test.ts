import { describe, expect, it } from 'vitest';
import { HIDDEN_LIVE_POLL_MS, MAX_BACKOFF_MS, VISIBLE_POLL_MS, pollDelayMs, shouldPoll } from './polling';
import { MAX_CONNECT_ATTEMPTS, reconnectDelayMs } from '../realtime/controller';

describe('pollDelayMs', () => {
  it('polls about every second while visible', () => {
    expect(pollDelayMs(0, true)).toBe(VISIBLE_POLL_MS);
    expect(VISIBLE_POLL_MS).toBe(1_000);
  });

  it('backs off on consecutive failures up to a cap', () => {
    expect(pollDelayMs(1, true)).toBe(2_000);
    expect(pollDelayMs(2, true)).toBe(4_000);
    expect(pollDelayMs(10, true)).toBe(MAX_BACKOFF_MS);
  });

  it('slows down while hidden', () => {
    expect(pollDelayMs(0, false)).toBe(HIDDEN_LIVE_POLL_MS);
  });
});

describe('shouldPoll', () => {
  it('polls while visible, and while hidden only during a live interview', () => {
    expect(shouldPoll(true, false)).toBe(true);
    expect(shouldPoll(false, true)).toBe(true);
    expect(shouldPoll(false, false)).toBe(false);
  });
});

describe('reconnectDelayMs', () => {
  it('backs off exponentially to 15 s', () => {
    expect([1, 2, 3, 4, 5, 6].map(reconnectDelayMs)).toEqual([1_000, 2_000, 4_000, 8_000, 15_000, 15_000]);
    expect(reconnectDelayMs(0)).toBe(1_000);
    expect(MAX_CONNECT_ATTEMPTS).toBeGreaterThan(1);
  });
});
