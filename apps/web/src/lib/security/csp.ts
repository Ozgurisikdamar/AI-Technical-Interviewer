/**
 * Per-request Content-Security-Policy with a nonce (built by src/proxy.ts).
 *
 * Next.js reads the nonce from the request's CSP header (`script-src 'nonce-…'`) during dynamic
 * rendering and applies it to its framework scripts, page bundles and inline flight-data scripts.
 * 'strict-dynamic' lets those nonce-bearing scripts load the lazily imported chunks
 * (e.g. livekit-client). No 'unsafe-inline' and no 'unsafe-eval' in production.
 */

export const DEFAULT_LIVEKIT_PUBLIC_URL = 'ws://localhost:7880';

const NONCE_PATTERN = /^[A-Za-z0-9+/_-]{16,}={0,2}$/;
const HOST_PATTERN = /^[a-z0-9.-]+(?::\d{1,5})?$/;

/** 128 random bits, base64 (the nonce-source grammar). Works in Node and browsers. */
export function createNonce(): string {
  const bytes = new Uint8Array(16);
  globalThis.crypto.getRandomValues(bytes);
  let binary = '';
  for (const b of bytes) binary += String.fromCharCode(b);
  return btoa(binary);
}

/**
 * The LiveKit signaling origin in both schemes livekit-client uses: WebSocket for signaling and
 * HTTP(S) for `/rtc/validate` and connectivity checks. Returns [] for anything that is not a plain
 * ws/wss/http/https origin, so a bad value can never inject CSP syntax.
 */
export function livekitConnectSources(raw: string | undefined): string[] {
  const value = (raw ?? '').trim() || DEFAULT_LIVEKIT_PUBLIC_URL;
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    return [];
  }
  if (url.username || url.password || !HOST_PATTERN.test(url.host)) return [];
  switch (url.protocol) {
    case 'ws:':
    case 'http:':
      return [`ws://${url.host}`, `http://${url.host}`];
    case 'wss:':
    case 'https:':
      return [`wss://${url.host}`, `https://${url.host}`];
    default:
      return [];
  }
}

export interface CspOptions {
  readonly nonce: string;
  /** `next dev` needs 'unsafe-eval' (React debug stacks) and inline styles (dev overlay). */
  readonly isDev: boolean;
  readonly livekitUrl: string | undefined;
}

export function buildContentSecurityPolicy({ nonce, isDev, livekitUrl }: CspOptions): string {
  if (!NONCE_PATTERN.test(nonce)) throw new Error('Invalid CSP nonce');
  const nonceSource = `'nonce-${nonce}'`;
  const directives: Array<[string, string[]]> = [
    ['default-src', ["'self'"]],
    ['script-src', ["'self'", nonceSource, "'strict-dynamic'", ...(isDev ? ["'unsafe-eval'"] : [])]],
    // A nonce disables 'unsafe-inline', so dev (overlay styles) uses 'unsafe-inline' without one.
    ['style-src', isDev ? ["'self'", "'unsafe-inline'"] : ["'self'", nonceSource]],
    ['img-src', isDev ? ["'self'", 'data:', 'blob:'] : ["'self'"]],
    ['font-src', ["'self'"]],
    ['connect-src', ["'self'", ...livekitConnectSources(livekitUrl)]],
    ['media-src', ["'self'"]],
    ['worker-src', ["'self'"]],
    ['manifest-src', ["'self'"]],
    ['frame-src', ["'none'"]],
    ['object-src', ["'none'"]],
    ['base-uri', ["'none'"]],
    ['form-action', ["'self'"]],
    ['frame-ancestors', ["'none'"]],
  ];
  return directives.map(([name, sources]) => `${name} ${sources.join(' ')}`).join('; ');
}
