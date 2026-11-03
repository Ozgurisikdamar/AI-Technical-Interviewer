/**
 * Input level meter math for the microphone check (Web Audio AnalyserNode time-domain samples).
 * Samples are floats in [-1, 1] (getFloatTimeDomainData). The meter shows a 0..1 value on a
 * decibel scale so that quiet speech still moves it visibly.
 */

export const METER_FLOOR_DB = -60;
export const METER_CEILING_DB = 0;
/** Below this, the signal is treated as digital silence (avoids log10(0)). */
export const SILENCE_DB = -100;

/** Root mean square of the samples (0 for an empty buffer). */
export function rms(samples: ArrayLike<number>): number {
  const n = samples.length;
  if (n === 0) return 0;
  let sum = 0;
  for (let i = 0; i < n; i++) {
    const s = samples[i] ?? 0;
    sum += s * s;
  }
  return Math.sqrt(sum / n);
}

/** Converts an RMS amplitude (full scale = 1) to dBFS, bounded below by SILENCE_DB. */
export function toDbfs(amplitude: number): number {
  if (!(amplitude > 0)) return SILENCE_DB;
  return Math.max(SILENCE_DB, 20 * Math.log10(amplitude));
}

/** Maps dBFS onto 0..1 between the floor and the ceiling, clamped. */
export function dbToLevel(db: number, floorDb = METER_FLOOR_DB, ceilingDb = METER_CEILING_DB): number {
  if (!(ceilingDb > floorDb)) throw new RangeError('ceiling must be above floor');
  if (Number.isNaN(db)) return 0;
  return Math.min(1, Math.max(0, (db - floorDb) / (ceilingDb - floorDb)));
}

/**
 * Peak-style ballistics: the meter rises quickly (attack) and falls slowly (release),
 * which reads as "the microphone hears me" without flicker.
 */
export function smoothLevel(previous: number, next: number, attack = 0.6, release = 0.12): number {
  const factor = next > previous ? attack : release;
  const value = previous + (next - previous) * factor;
  return Math.min(1, Math.max(0, value));
}

/** One analyser frame to a meter level. */
export function levelFromSamples(samples: ArrayLike<number>, previous = 0): number {
  return smoothLevel(previous, dbToLevel(toDbfs(rms(samples))));
}

/**
 * Level above which we tell the candidate their voice is being picked up (-36 dBFS).
 * An uncalibrated heuristic for the device check only; it never affects the interview.
 */
export const VOICE_DETECTED_LEVEL = 0.4;

export function isVoiceDetected(level: number): boolean {
  return level >= VOICE_DETECTED_LEVEL;
}
