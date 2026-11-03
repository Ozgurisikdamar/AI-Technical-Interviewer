/**
 * Short-polling cadence for GET /api/v1/interviews/{id}: about every second while the page is
 * visible, backing off on consecutive failures. While hidden, polling continues slowly only during
 * a live interview so that a server-side pause still stops the microphone; otherwise it stops.
 */

export const VISIBLE_POLL_MS = 1_000;
export const HIDDEN_LIVE_POLL_MS = 5_000;
export const MAX_BACKOFF_MS = 10_000;

export function pollDelayMs(consecutiveFailures: number, visible: boolean): number {
  const base = visible ? VISIBLE_POLL_MS : HIDDEN_LIVE_POLL_MS;
  if (consecutiveFailures <= 0) return base;
  return Math.min(MAX_BACKOFF_MS, base * 2 ** Math.min(consecutiveFailures, 4));
}

export function shouldPoll(visible: boolean, live: boolean): boolean {
  return visible || live;
}
