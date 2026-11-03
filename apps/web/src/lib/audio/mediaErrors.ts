/**
 * Classifies getUserMedia failures (DOMException names per the Media Capture spec, plus legacy
 * Chrome names) into the cases the UI explains to the candidate.
 */

export type MediaErrorKind =
  | 'permission_denied'
  | 'no_device'
  | 'device_in_use'
  | 'insecure_context'
  | 'device_lost'
  | 'unknown';

/** Capture processing for both the device check and the published interview track. */
export const AUDIO_CONSTRAINTS = {
  echoCancellation: true,
  noiseSuppression: true,
  autoGainControl: true,
} as const satisfies MediaTrackConstraints;

export function classifyMediaError(error: unknown): MediaErrorKind {
  const name =
    typeof error === 'object' && error !== null && 'name' in error ? String((error as { name: unknown }).name) : '';
  switch (name) {
    case 'NotAllowedError':
    case 'PermissionDeniedError':
    case 'SecurityError':
      return 'permission_denied';
    case 'NotFoundError':
    case 'DevicesNotFoundError':
    case 'OverconstrainedError':
    case 'ConstraintNotSatisfiedError':
      return 'no_device';
    case 'NotReadableError':
    case 'TrackStartError':
    case 'AbortError':
      return 'device_in_use';
    case 'TypeError':
      return 'insecure_context';
    default:
      return 'unknown';
  }
}

/** getUserMedia exists only in secure contexts (https or localhost). */
export function microphoneApiAvailable(): boolean {
  return (
    typeof navigator !== 'undefined' &&
    typeof navigator.mediaDevices !== 'undefined' &&
    typeof navigator.mediaDevices.getUserMedia === 'function'
  );
}
