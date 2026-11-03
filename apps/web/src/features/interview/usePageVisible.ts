'use client';

import { useSyncExternalStore } from 'react';

function subscribe(onChange: () => void): () => void {
  document.addEventListener('visibilitychange', onChange);
  return () => document.removeEventListener('visibilitychange', onChange);
}

function getSnapshot(): boolean {
  return document.visibilityState !== 'hidden';
}

function getServerSnapshot(): boolean {
  return true;
}

/** True while the document is visible (Page Visibility API). */
export function usePageVisible(): boolean {
  return useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);
}
