import { describe, expect, it } from 'vitest';
import {
  METER_FLOOR_DB,
  SILENCE_DB,
  dbToLevel,
  isVoiceDetected,
  levelFromSamples,
  rms,
  smoothLevel,
  toDbfs,
} from './level';

function sine(amplitude: number, n = 4_800, cycles = 10): Float32Array {
  const out = new Float32Array(n);
  for (let i = 0; i < n; i++) out[i] = amplitude * Math.sin((2 * Math.PI * cycles * i) / n);
  return out;
}

describe('rms', () => {
  it('is 0 for silence and for an empty buffer', () => {
    expect(rms(new Float32Array(1024))).toBe(0);
    expect(rms([])).toBe(0);
  });

  it('equals the magnitude of a constant signal', () => {
    expect(rms([0.5, -0.5, 0.5, -0.5])).toBeCloseTo(0.5, 10);
  });

  it('is amplitude/sqrt(2) for a sine wave', () => {
    expect(rms(sine(1))).toBeCloseTo(Math.SQRT1_2, 3);
    expect(rms(sine(0.1))).toBeCloseTo(0.1 * Math.SQRT1_2, 4);
  });
});

describe('toDbfs', () => {
  it('maps full scale to 0 dBFS and halves to about -6 dB', () => {
    expect(toDbfs(1)).toBeCloseTo(0, 10);
    expect(toDbfs(0.5)).toBeCloseTo(-6.0206, 3);
    expect(toDbfs(0.001)).toBeCloseTo(-60, 6);
  });

  it('bounds silence and invalid input', () => {
    expect(toDbfs(0)).toBe(SILENCE_DB);
    expect(toDbfs(-1)).toBe(SILENCE_DB);
    expect(toDbfs(Number.NaN)).toBe(SILENCE_DB);
    expect(toDbfs(1e-12)).toBe(SILENCE_DB);
  });
});

describe('dbToLevel', () => {
  it('maps the floor..ceiling range onto 0..1 and clamps outside it', () => {
    expect(dbToLevel(METER_FLOOR_DB)).toBe(0);
    expect(dbToLevel(0)).toBe(1);
    expect(dbToLevel(-30)).toBeCloseTo(0.5, 10);
    expect(dbToLevel(-100)).toBe(0);
    expect(dbToLevel(6)).toBe(1);
    expect(dbToLevel(Number.NaN)).toBe(0);
  });

  it('rejects an empty range', () => {
    expect(() => dbToLevel(-10, 0, 0)).toThrow(RangeError);
  });
});

describe('smoothLevel', () => {
  it('rises faster than it falls', () => {
    const up = smoothLevel(0, 1);
    const down = 1 - smoothLevel(1, 0);
    expect(up).toBeGreaterThan(down);
    expect(up).toBeCloseTo(0.6, 10);
    expect(smoothLevel(1, 0)).toBeCloseTo(0.88, 10);
  });

  it('stays within 0..1', () => {
    expect(smoothLevel(0.5, 2, 1, 1)).toBe(1);
    expect(smoothLevel(0.5, -2, 1, 1)).toBe(0);
  });

  it('converges to a steady input', () => {
    let level = 0;
    for (let i = 0; i < 60; i++) level = smoothLevel(level, 0.7);
    expect(level).toBeCloseTo(0.7, 6);
  });
});

describe('levelFromSamples and voice detection', () => {
  it('shows nothing for silence and a clear level for normal speech volume', () => {
    expect(levelFromSamples(new Float32Array(2048))).toBe(0);
    // A -20 dBFS RMS signal (normal speech into a microphone with AGC) settles near 0.67.
    let level = 0;
    const speech = sine(0.1 * Math.SQRT2);
    for (let i = 0; i < 20; i++) level = levelFromSamples(speech, level);
    expect(level).toBeCloseTo(2 / 3, 2);
    expect(isVoiceDetected(level)).toBe(true);
  });

  it('does not report a voice for low background noise (-55 dBFS)', () => {
    let level = 0;
    const hiss = sine(10 ** (-55 / 20) * Math.SQRT2);
    for (let i = 0; i < 20; i++) level = levelFromSamples(hiss, level);
    expect(isVoiceDetected(level)).toBe(false);
  });
});
