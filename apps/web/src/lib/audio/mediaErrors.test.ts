import { describe, expect, it } from 'vitest';
import { AUDIO_CONSTRAINTS, classifyMediaError, explainMediaError, microphonePermission } from './mediaErrors';

const named = (name: string) => Object.assign(new Error('x'), { name });

describe('classifyMediaError', () => {
  it.each([
    ['NotAllowedError', 'permission_denied'],
    ['PermissionDeniedError', 'permission_denied'],
    ['SecurityError', 'permission_denied'],
    ['NotFoundError', 'no_device'],
    ['DevicesNotFoundError', 'no_device'],
    ['OverconstrainedError', 'no_device'],
    ['NotReadableError', 'device_in_use'],
    ['TrackStartError', 'device_in_use'],
    ['AbortError', 'device_in_use'],
    ['TypeError', 'insecure_context'],
    ['SomethingElse', 'unknown'],
  ])('%s -> %s', (name, kind) => {
    expect(classifyMediaError(named(name))).toBe(kind);
  });

  it('handles DOMException and non-errors', () => {
    expect(classifyMediaError(new DOMException('denied', 'NotAllowedError'))).toBe('permission_denied');
    expect(classifyMediaError(undefined)).toBe('unknown');
    expect(classifyMediaError('NotAllowedError')).toBe('unknown');
  });
});

describe('capture constraints', () => {
  it('turn on echo cancellation, noise suppression and automatic gain control', () => {
    expect(AUDIO_CONSTRAINTS).toEqual({ echoCancellation: true, noiseSuppression: true, autoGainControl: true });
  });
});

describe('explainMediaError', () => {
  const permissions = (state: string) => ({ query: async () => ({ state }) });
  const unsupported = { query: async () => Promise.reject(new TypeError('microphone is not a valid permission name')) };

  it('explains an unclear error as blocked when the browser says the microphone is denied', async () => {
    // Headless Chromium reports a refused microphone as NotSupportedError.
    await expect(explainMediaError(named('NotSupportedError'), permissions('denied'))).resolves.toBe('permission_denied');
    await expect(explainMediaError(named('NotReadableError'), permissions('denied'))).resolves.toBe('permission_denied');
  });

  it('keeps the classification otherwise', async () => {
    await expect(explainMediaError(named('NotSupportedError'), permissions('prompt'))).resolves.toBe('unknown');
    await expect(explainMediaError(named('NotReadableError'), permissions('granted'))).resolves.toBe('device_in_use');
    await expect(explainMediaError(named('NotSupportedError'), unsupported)).resolves.toBe('unknown');
    await expect(explainMediaError(named('NotSupportedError'), undefined)).resolves.toBe('unknown');
  });

  it('never re-labels a clear error', async () => {
    await expect(explainMediaError(named('NotFoundError'), permissions('denied'))).resolves.toBe('no_device');
    await expect(explainMediaError(named('TypeError'), permissions('denied'))).resolves.toBe('insecure_context');
    await expect(explainMediaError(named('NotAllowedError'), permissions('granted'))).resolves.toBe('permission_denied');
  });
});

describe('microphonePermission', () => {
  it('returns null when the browser cannot tell', async () => {
    await expect(microphonePermission(undefined)).resolves.toBeNull();
    await expect(microphonePermission({ query: async () => Promise.reject(new Error('x')) })).resolves.toBeNull();
    await expect(microphonePermission({ query: async () => ({ state: 'granted' }) })).resolves.toBe('granted');
  });
});
