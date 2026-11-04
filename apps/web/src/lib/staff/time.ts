/**
 * Times in the review console are shown in UTC, labelled as such, so server and client render
 * the same text and reviewers in different time zones read the same value.
 */
const DATE_TIME = new Intl.DateTimeFormat('en-GB', {
  year: 'numeric',
  month: 'short',
  day: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
  hourCycle: 'h23',
  timeZone: 'UTC',
});

/** "4 Nov 2026, 13:13 UTC", or "—" for a value that is not a time. */
export function formatDateTime(iso: string): string {
  const at = Date.parse(iso);
  return Number.isNaN(at) ? '—' : `${DATE_TIME.format(at)} UTC`;
}

/** A transcript offset: 83_500 ms → "1:23". */
export function formatOffset(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000));
  const minutes = Math.floor(total / 60);
  const seconds = total % 60;
  return `${minutes}:${seconds.toString().padStart(2, '0')}`;
}
