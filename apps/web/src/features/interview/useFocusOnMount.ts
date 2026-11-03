'use client';

import { useEffect, useRef } from 'react';

/**
 * Moves focus to the step heading when a new step appears after a state change, so keyboard and
 * screen-reader users land at the start of the new content. Not used on the first page load.
 */
export function useFocusOnMount<T extends HTMLElement>(enabled: boolean) {
  const ref = useRef<T | null>(null);
  useEffect(() => {
    if (enabled) ref.current?.focus();
  }, [enabled]);
  return ref;
}
