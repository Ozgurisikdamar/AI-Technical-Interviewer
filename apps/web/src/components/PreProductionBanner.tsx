'use client';

import { useSyncExternalStore } from 'react';
import { useMessages } from '@/i18n/client';
import {
  getApiEnvironment,
  getServerApiEnvironment,
  normalizeEnvironment,
  shouldShowPreProductionBanner,
  subscribeApiEnvironment,
} from '@/lib/environment';
import styles from './PreProductionBanner.module.css';

/**
 * Persistent "pre-production" banner (ADR-020). Shown unless both the web server (APP_ENV) and
 * the control plane (environment / X-ATI-Environment) say "production"; unknown means shown.
 */
export function PreProductionBanner({ webEnvironment }: { webEnvironment: string | undefined }) {
  const t = useMessages();
  const apiEnvironment = useSyncExternalStore(subscribeApiEnvironment, getApiEnvironment, getServerApiEnvironment);
  if (!shouldShowPreProductionBanner(normalizeEnvironment(webEnvironment), apiEnvironment)) return null;
  return (
    <div className={styles.banner} role="note" data-testid="preprod-banner">
      <strong>{t.banner.preProduction}</strong>
    </div>
  );
}
