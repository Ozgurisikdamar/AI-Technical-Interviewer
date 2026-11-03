/**
 * Environment awareness for the persistent pre-production banner (ADR-020).
 *
 * Two sources are combined, failing safe:
 * - the web server's own APP_ENV, rendered into the page on the server (no flash in production), and
 * - the control plane's environment (`environment` in the candidate view, or the
 *   X-ATI-Environment header on any API response).
 * The banner shows unless the web server says "production" and the API has not reported otherwise.
 */
import { ENVIRONMENTS, type Environment } from '@/lib/api/types';

export type ReportedEnvironment = Environment | 'unknown';

export function normalizeEnvironment(value: string | null | undefined): ReportedEnvironment | undefined {
  if (value === null || value === undefined) return undefined;
  const trimmed = value.trim().toLowerCase();
  if (trimmed === '') return undefined;
  return (ENVIRONMENTS as readonly string[]).includes(trimmed) ? (trimmed as Environment) : 'unknown';
}

export function shouldShowPreProductionBanner(
  webEnvironment: ReportedEnvironment | undefined,
  apiEnvironment: ReportedEnvironment | undefined,
): boolean {
  if (webEnvironment !== 'production') return true;
  return apiEnvironment !== undefined && apiEnvironment !== 'production';
}

let apiEnvironment: ReportedEnvironment | undefined;
const listeners = new Set<() => void>();

/** Records the environment reported by the API. Safe to call with any header value. */
export function reportApiEnvironment(value: string | null | undefined): void {
  const next = normalizeEnvironment(value);
  if (next === undefined || next === apiEnvironment) return;
  apiEnvironment = next;
  for (const listener of listeners) listener();
}

export function subscribeApiEnvironment(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function getApiEnvironment(): ReportedEnvironment | undefined {
  return apiEnvironment;
}

export function getServerApiEnvironment(): ReportedEnvironment | undefined {
  return undefined;
}

/** Test helper. */
export function resetApiEnvironmentForTests(): void {
  apiEnvironment = undefined;
}
