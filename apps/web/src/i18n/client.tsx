'use client';

import { createContext, useContext, type ReactNode } from 'react';
import { DEFAULT_LOCALE, getMessages, type Locale, type Messages } from './index';

const MessagesContext = createContext<Messages>(getMessages(DEFAULT_LOCALE));

/** Provides the catalog for a locale. Catalogs hold functions, so only the locale crosses the server boundary. */
export function MessagesProvider({ locale, children }: { locale: Locale; children: ReactNode }) {
  return <MessagesContext.Provider value={getMessages(locale)}>{children}</MessagesContext.Provider>;
}

export function useMessages(): Messages {
  return useContext(MessagesContext);
}
