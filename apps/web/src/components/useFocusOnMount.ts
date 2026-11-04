'use client';

import { useEffect, useRef } from 'react';

/**
 * Moves focus to an element (a step's heading, a notice) when it appears, so keyboard and
 * screen-reader users land at the start of the new content. The element needs tabIndex={-1}
 * unless it is focusable already. `enabled` false leaves focus where it is, e.g. on a page's
 * first load.
 */
export function useFocusOnMount<T extends HTMLElement>(enabled: boolean) {
  const ref = useRef<T | null>(null);
  useEffect(() => {
    if (enabled) ref.current?.focus();
  }, [enabled]);
  return ref;
}
