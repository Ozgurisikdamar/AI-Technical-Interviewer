'use client';

import { createContext, useContext } from 'react';
import { useMessages } from '@/i18n/client';
import { errorMessage, type ErrorCatalog } from '@/lib/api/errorMessages';
import type { ApiError } from '@/lib/api/errors';
import ui from './ui.module.css';

/** The error messages for the pages below: the candidate's by default, the review console's inside it. */
const ErrorCatalogContext = createContext<ErrorCatalog | null>(null);
export const ErrorCatalogProvider = ErrorCatalogContext.Provider;

interface ErrorNoticeProps {
  error: Pick<ApiError, 'code' | 'status' | 'requestId'>;
  onRetry?: () => void;
  retryLabel?: string;
}

/** Friendly, fixed message for an API error, with the correlation ID for support. Never shows server text. */
export function ErrorNotice({ error, onRetry, retryLabel }: ErrorNoticeProps) {
  const t = useMessages();
  const catalog = useContext(ErrorCatalogContext) ?? t.errors;
  return (
    <div role="alert" className={ui.error}>
      <p>{errorMessage(catalog, error)}</p>
      {error.requestId !== null ? <p className={ui.reference}>{t.common.reference(error.requestId)}</p> : null}
      {onRetry ? (
        <p>
          <button type="button" className={`${ui.button} ${ui.secondary}`} onClick={onRetry}>
            {retryLabel ?? t.common.retry}
          </button>
        </p>
      ) : null}
    </div>
  );
}
