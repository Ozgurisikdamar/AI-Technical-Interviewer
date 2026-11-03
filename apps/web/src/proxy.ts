import { NextResponse, type NextRequest } from 'next/server';
import { buildContentSecurityPolicy, createNonce } from '@/lib/security/csp';

/**
 * Next.js 16 Proxy (formerly middleware): a fresh CSP nonce for every document request.
 * The CSP is set on the request (Next.js extracts the nonce from it while rendering) and on the
 * response. Pages are rendered dynamically (see app/layout.tsx), so every response carries the
 * nonce that matches its own scripts.
 */
export function proxy(request: NextRequest) {
  const nonce = createNonce();
  const csp = buildContentSecurityPolicy({
    nonce,
    isDev: process.env.NODE_ENV === 'development',
    livekitUrl: process.env.ATI_LIVEKIT_PUBLIC_URL,
  });

  const requestHeaders = new Headers(request.headers);
  requestHeaders.set('x-nonce', nonce);
  requestHeaders.set('Content-Security-Policy', csp);

  const response = NextResponse.next({ request: { headers: requestHeaders } });
  response.headers.set('Content-Security-Policy', csp);
  return response;
}

export const config = {
  matcher: [
    {
      // Everything except the API rewrite (the control plane sets its own headers) and static
      // build assets. Prefetches carry no document, so they need no policy.
      source: '/((?!api/|_next/static|_next/image|favicon.ico).*)',
      missing: [
        { type: 'header', key: 'next-router-prefetch' },
        { type: 'header', key: 'purpose', value: 'prefetch' },
      ],
    },
  ],
};
