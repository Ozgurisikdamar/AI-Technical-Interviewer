import { describe, expect, it } from 'vitest';
import { AUDIO_CONSTRAINTS, classifyMediaError } from './mediaErrors';

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
