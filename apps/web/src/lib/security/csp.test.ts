import { describe, expect, it } from 'vitest';
import { buildContentSecurityPolicy, createNonce, livekitConnectSources } from './csp';

function directives(csp: string): Map<string, string[]> {
  return new Map(
    csp.split(';').map((d) => {
      const [name = '', ...sources] = d.trim().split(/\s+/);
      return [name, sources];
    }),
  );
}

describe('createNonce', () => {
  it('produces fresh base64 nonces with 128 bits of entropy', () => {
    const a = createNonce();
    const b = createNonce();
    expect(a).not.toBe(b);
    expect(a).toMatch(/^[A-Za-z0-9+/]{22}==$/);
  });
});

describe('livekitConnectSources', () => {
  it('allows the signaling origin over WebSocket and HTTP', () => {
    expect(livekitConnectSources('ws://localhost:7880')).toEqual(['ws://localhost:7880', 'http://localhost:7880']);
    expect(livekitConnectSources('wss://lk.example.invalid/some/path')).toEqual([
      'wss://lk.example.invalid',
      'https://lk.example.invalid',
    ]);
  });

  it('defaults to the local LiveKit server', () => {
    expect(livekitConnectSources(undefined)).toEqual(['ws://localhost:7880', 'http://localhost:7880']);
    expect(livekitConnectSources('  ')).toEqual(['ws://localhost:7880', 'http://localhost:7880']);
  });

  it.each([
    "ws://evil.example.invalid; script-src 'unsafe-inline'",
    'javascript:alert(1)',
    'ftp://lk.example.invalid',
    'wss://user:secret@lk.example.invalid',
    'not a url',
    'ws://[::1]:7880',
  ])('never lets %s inject CSP syntax', (value) => {
    expect(livekitConnectSources(value)).toEqual([]);
  });
});

describe('buildContentSecurityPolicy', () => {
  const nonce = 'AAAAAAAAAAAAAAAAAAAAAA==';
  const prod = directives(buildContentSecurityPolicy({ nonce, isDev: false, livekitUrl: 'wss://lk.example.invalid' }));

  it('uses the per-request nonce with strict-dynamic and no unsafe-inline or unsafe-eval for scripts', () => {
    expect(prod.get('script-src')).toEqual(["'self'", `'nonce-${nonce}'`, "'strict-dynamic'"]);
    expect(prod.get('style-src')).toEqual(["'self'", `'nonce-${nonce}'`]);
  });

  it('allows connections only to this origin and the LiveKit origin', () => {
    expect(prod.get('connect-src')).toEqual(["'self'", 'wss://lk.example.invalid', 'https://lk.example.invalid']);
  });

  it('forbids framing, plugins, base rewriting and frames', () => {
    expect(prod.get('frame-ancestors')).toEqual(["'none'"]);
    expect(prod.get('object-src')).toEqual(["'none'"]);
    expect(prod.get('base-uri')).toEqual(["'none'"]);
    expect(prod.get('frame-src')).toEqual(["'none'"]);
    expect(prod.get('default-src')).toEqual(["'self'"]);
    expect(prod.get('form-action')).toEqual(["'self'"]);
  });

  it('relaxes only what next dev needs', () => {
    const dev = directives(buildContentSecurityPolicy({ nonce, isDev: true, livekitUrl: undefined }));
    expect(dev.get('script-src')).toContain("'unsafe-eval'");
    expect(dev.get('style-src')).toEqual(["'self'", "'unsafe-inline'"]);
  });

  it('refuses a malformed nonce', () => {
    expect(() => buildContentSecurityPolicy({ nonce: "x' 'unsafe-inline", isDev: false, livekitUrl: undefined })).toThrow();
  });
});
