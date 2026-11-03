/**
 * Maps a problem `code` (or, failing that, the HTTP status) to a friendly, fixed message from the
 * catalog. Server-provided `title`/`detail` text is never shown: it can echo user input.
 */
import type { Messages } from '@/i18n';
import type { ApiError } from './errors';

export function errorMessage(messages: Messages, error: Pick<ApiError, 'code' | 'status'>): string {
  const { byCode, byStatus, serverError, generic } = messages.errors;
  const byCodeMessage = Object.prototype.hasOwnProperty.call(byCode, error.code) ? byCode[error.code] : undefined;
  if (byCodeMessage !== undefined) return byCodeMessage;
  const byStatusMessage = Object.prototype.hasOwnProperty.call(byStatus, error.status) ? byStatus[error.status] : undefined;
  if (byStatusMessage !== undefined) return byStatusMessage;
  if (error.status >= 500) return serverError;
  return generic;
}
