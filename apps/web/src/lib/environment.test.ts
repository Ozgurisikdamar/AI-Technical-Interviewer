import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  getApiEnvironment,
  normalizeEnvironment,
  reportApiEnvironment,
  resetApiEnvironmentForTests,
  shouldShowPreProductionBanner,
  subscribeApiEnvironment,
} from './environment';

beforeEach(() => resetApiEnvironmentForTests());

describe('pre-production banner (ADR-020)', () => {
  it('shows whenever the web server is not configured as production', () => {
    expect(shouldShowPreProductionBanner(undefined, undefined)).toBe(true);
    expect(shouldShowPreProductionBanner('dev', 'production')).toBe(true);
    expect(shouldShowPreProductionBanner('unknown', 'production')).toBe(true);
  });

  it('shows whenever the API reports a non-production environment', () => {
    expect(shouldShowPreProductionBanner('production', 'dev')).toBe(true);
    expect(shouldShowPreProductionBanner('production', 'staging')).toBe(true);
    expect(shouldShowPreProductionBanner('production', 'unknown')).toBe(true);
  });

  it('hides only when both agree on production (no flash before the API answers)', () => {
    expect(shouldShowPreProductionBanner('production', 'production')).toBe(false);
    expect(shouldShowPreProductionBanner('production', undefined)).toBe(false);
  });
});

describe('environment store', () => {
  it('normalises values and treats unrecognised ones as unknown', () => {
    expect(normalizeEnvironment(' Production ')).toBe('production');
    expect(normalizeEnvironment('prod')).toBe('unknown');
    expect(normalizeEnvironment('')).toBeUndefined();
    expect(normalizeEnvironment(null)).toBeUndefined();
  });

  it('notifies subscribers on change only', () => {
    const listener = vi.fn();
    const unsubscribe = subscribeApiEnvironment(listener);
    reportApiEnvironment('dev');
    reportApiEnvironment('dev');
    reportApiEnvironment(null);
    expect(listener).toHaveBeenCalledTimes(1);
    expect(getApiEnvironment()).toBe('dev');
    unsubscribe();
    reportApiEnvironment('production');
    expect(listener).toHaveBeenCalledTimes(1);
  });
});
