import type { NextConfig } from 'next';

/**
 * Server-side only. The browser never sees this value: it only ever calls same-origin
 * `/api/v1/...` paths, which this rewrite forwards to the control plane's public listener.
 * The internal listener (`/internal/v1`, health, metrics) is a different port and is never
 * routed here (ADR-016).
 *
 * Note: `next start` serves rewrites from the build-time routes manifest, so this value is
 * captured when `next build` runs. Set it in the build environment.
 */
const DEFAULT_CONTROL_PLANE_URL = 'http://127.0.0.1:8080';

function controlPlaneOrigin(raw: string | undefined): string {
  const value = (raw ?? '').trim() || DEFAULT_CONTROL_PLANE_URL;
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    throw new Error('ATI_CONTROL_PLANE_URL must be an absolute http(s) URL');
  }
  if (url.protocol !== 'http:' && url.protocol !== 'https:') {
    throw new Error('ATI_CONTROL_PLANE_URL must use http or https');
  }
  if (url.username || url.password || url.search || url.hash || (url.pathname !== '/' && url.pathname !== '')) {
    throw new Error('ATI_CONTROL_PLANE_URL must be an origin only (no credentials, path, query or fragment)');
  }
  return url.origin;
}

const controlPlane = controlPlaneOrigin(process.env.ATI_CONTROL_PLANE_URL);

/** Static security headers for every response. The CSP is per request (nonce) and set in src/proxy.ts. */
const securityHeaders = [
  { key: 'Permissions-Policy', value: 'microphone=(self), camera=(), geolocation=()' },
  { key: 'Referrer-Policy', value: 'no-referrer' },
  { key: 'X-Content-Type-Options', value: 'nosniff' },
  { key: 'X-Frame-Options', value: 'DENY' },
  { key: 'Cross-Origin-Opener-Policy', value: 'same-origin' },
  { key: 'Cross-Origin-Resource-Policy', value: 'same-origin' },
  { key: 'X-DNS-Prefetch-Control', value: 'off' },
];

/**
 * The review console (ADR-023) never uses the microphone and must never be indexed. Listed after
 * the global rule: when two rules set the same header, the later one wins.
 */
const staffHeaders = [
  { key: 'Permissions-Policy', value: 'microphone=(), camera=(), geolocation=()' },
  { key: 'X-Robots-Tag', value: 'noindex, nofollow' },
];

const nextConfig: NextConfig = {
  reactStrictMode: true,
  poweredByHeader: false,
  productionBrowserSourceMaps: false,
  async rewrites() {
    return [
      {
        source: '/api/:path*',
        destination: `${controlPlane}/api/:path*`,
      },
    ];
  },
  async headers() {
    return [
      {
        source: '/:path*',
        headers: securityHeaders,
      },
      {
        source: '/staff/:path*',
        headers: staffHeaders,
      },
    ];
  },
};

export default nextConfig;
