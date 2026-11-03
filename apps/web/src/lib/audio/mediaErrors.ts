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

type PermissionsLike = { query(descriptor: { name: string }): Promise<{ state: string }> } | undefined;

/** The microphone permission as the browser reports it; null when it cannot say (e.g. Firefox). */
export async function microphonePermission(permissions: PermissionsLike = globalThis.navigator?.permissions): Promise<string | null> {
  try {
    return permissions ? (await permissions.query({ name: 'microphone' })).state : null;
  } catch {
    return null;
  }
}

/**
 * The failure explained to the candidate. Browsers do not agree on the error a blocked microphone
 * raises (headless Chromium reports NotSupportedError), so an unclear error is checked against
 * the Permissions API: a blocked microphone must be explained as blocked, with the fix (QA-AN-01).
 */
export async function explainMediaError(error: unknown, permissions?: PermissionsLike): Promise<MediaErrorKind> {
  const kind = classifyMediaError(error);
  if (kind === 'permission_denied' || kind === 'insecure_context' || kind === 'no_device') return kind;
  return (await microphonePermission(permissions)) === 'denied' ? 'permission_denied' : kind;
}

/** getUserMedia exists only in secure contexts (https or localhost). */
export function microphoneApiAvailable(): boolean {
  return (
    typeof navigator !== 'undefined' &&
    typeof navigator.mediaDevices !== 'undefined' &&
    typeof navigator.mediaDevices.getUserMedia === 'function'
  );
}
