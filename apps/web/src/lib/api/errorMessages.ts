/**
 * Maps a problem `code` (or, failing that, the HTTP status) to a friendly, fixed message from the
 * catalog. Server-provided `title`/`detail` text is never shown: it can echo user input.
 */
import type { Messages } from '@/i18n';
import { label } from '@/i18n/label';
import type { ApiError } from './errors';

/** A catalog of error messages: the candidate's (`errors`) or the review console's (`staff.errors`). */
export type ErrorCatalog = Messages['errors'];

export function errorMessage(catalog: ErrorCatalog, error: Pick<ApiError, 'code' | 'status'>): string {
  return (
    label(catalog.byCode, error.code) ??
    label(catalog.byStatus, error.status) ??
    (error.status >= 500 ? catalog.serverError : catalog.generic)
  );
}
