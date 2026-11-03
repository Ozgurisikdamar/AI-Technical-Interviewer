/**
 * Same-origin API client. This module is the only place in the app allowed to call fetch
 * (enforced by ESLint). Guarantees:
 * - only relative `/api/v1/...` paths are requested (never absolute URLs, never `/internal/`);
 * - `credentials: 'same-origin'` and `mode: 'same-origin'`, so the HttpOnly session cookie is sent
 *   only to this origin and a cross-origin URL is refused by the browser as well;
 * - every POST carries a fresh `Idempotency-Key` and `Content-Type: application/json`;
 * - errors are parsed as RFC 9457 problem documents into an ApiError holding only the stable code.
 */
import { reportApiEnvironment } from '@/lib/environment';
import { ApiError, PROBLEM_CODE_PATTERN, REQUEST_ID_PATTERN, isAbortError } from './errors';

export const API_BASE = '/api/v1/';
const DEFAULT_TIMEOUT_MS = 15_000;

/** Characters allowed in our API paths: segment text, dashes, dots inside segments and slashes. */
const SAFE_PATH = /^\/api\/v1\/[A-Za-z0-9_\-/.]+$/;

/**
 * Throws unless `path` is a relative, normalised `/api/v1/...` path. Rejects absolute and
 * protocol-relative URLs, dot segments, encoded separators, query strings and the internal API.
 */
export function assertApiPath(path: string): string {
  const ok =
    typeof path === 'string' &&
    path.startsWith(API_BASE) &&
    SAFE_PATH.test(path) &&
    !path.includes('//') &&
    !/(^|\/)\.{1,2}(\/|$)/.test(path) &&
    !/\/internal(\/|$)/i.test(path);
  if (!ok) {
    throw new Error('Refusing to call a non-public or non-relative API path');
  }
  return path;
}

/** A fresh idempotency key per request (matches ^[A-Za-z0-9_.:-]{8,128}$). */
export function newIdempotencyKey(): string {
  const c = globalThis.crypto;
  if (typeof c?.randomUUID === 'function') {
    return c.randomUUID();
  }
  // crypto.randomUUID needs a secure context; getRandomValues does not.
  const bytes = new Uint8Array(16);
  c.getRandomValues(bytes);
  bytes[6] = ((bytes[6] ?? 0) & 0x0f) | 0x40;
  bytes[8] = ((bytes[8] ?? 0) & 0x3f) | 0x80;
  const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

export interface RequestOptions {
  /** Aborting rejects with the original AbortError (callers usually ignore it). */
  signal?: AbortSignal;
  timeoutMs?: number;
}

type Parser<T> = (body: unknown) => T;

export function getJson<T>(path: string, parse: Parser<T>, options: RequestOptions = {}): Promise<T> {
  return request('GET', path, undefined, parse, options);
}

export function postJson<T>(path: string, body: unknown, parse: Parser<T>, options: RequestOptions = {}): Promise<T> {
  return request('POST', path, body ?? {}, parse, options);
}

async function request<T>(
  method: 'GET' | 'POST',
  path: string,
  body: unknown,
  parse: Parser<T>,
  options: RequestOptions,
): Promise<T> {
  assertApiPath(path);

  const headers: Record<string, string> = { Accept: 'application/json, application/problem+json' };
  let payload: string | undefined;
  if (method === 'POST') {
    headers['Content-Type'] = 'application/json';
    headers['Idempotency-Key'] = newIdempotencyKey();
    payload = JSON.stringify(body);
  }

  const controller = new AbortController();
  let timedOut = false;
  const timer = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, options.timeoutMs ?? DEFAULT_TIMEOUT_MS);
  const onCallerAbort = () => controller.abort();
  if (options.signal?.aborted) controller.abort();
  options.signal?.addEventListener('abort', onCallerAbort, { once: true });

  try {
    let response: Response;
    try {
      response = await fetch(path, {
        method,
        headers,
        body: payload,
        credentials: 'same-origin',
        mode: 'same-origin',
        cache: 'no-store',
        redirect: 'error',
        referrerPolicy: 'no-referrer',
        signal: controller.signal,
      });
    } catch (error) {
      if (timedOut) throw new ApiError(0, 'timeout');
      if (options.signal?.aborted && isAbortError(error)) throw error;
      throw new ApiError(0, 'network_error');
    }

    reportApiEnvironment(response.headers.get('X-ATI-Environment'));
    const headerRequestId = response.headers.get('X-Request-ID');

    if (!response.ok) {
      throw await problemFromResponse(response, headerRequestId);
    }

    let json: unknown;
    try {
      json = await response.json();
    } catch (error) {
      if (timedOut) throw new ApiError(0, 'timeout');
      if (options.signal?.aborted && isAbortError(error)) throw error;
      throw new ApiError(response.status, 'invalid_response', headerRequestId);
    }
    try {
      return parse(json);
    } catch {
      throw new ApiError(response.status, 'invalid_response', headerRequestId);
    }
  } finally {
    clearTimeout(timer);
    options.signal?.removeEventListener('abort', onCallerAbort);
  }
}

/** Parses an RFC 9457 problem document, keeping only `code` and `request_id`. */
export async function problemFromResponse(response: Response, headerRequestId: string | null = null): Promise<ApiError> {
  let code = 'unknown';
  let requestId = headerRequestId;
  const contentType = response.headers.get('Content-Type') ?? '';
  if (/^application\/(problem\+)?json\b/i.test(contentType)) {
    try {
      const body: unknown = await response.json();
      if (typeof body === 'object' && body !== null) {
        const record = body as Record<string, unknown>;
        if (typeof record.code === 'string' && PROBLEM_CODE_PATTERN.test(record.code)) {
          code = record.code;
        }
        if (typeof record.request_id === 'string' && REQUEST_ID_PATTERN.test(record.request_id)) {
          requestId = record.request_id;
        }
      }
    } catch {
      // Unparseable problem body: keep the status-only error.
    }
  }
  return new ApiError(response.status, code, requestId);
}
