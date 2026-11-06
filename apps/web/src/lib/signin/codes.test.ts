import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { CODE_LENGTH, NUMBER_LENGTH, normalizeCode, normalizeNumber, normalizeSymbols } from './codes';

interface Case {
  name: string;
  kind: 'number' | 'code';
  input: string;
  canonical: string | null;
}

/** The cases the control plane runs too (services/control-plane/internal/accesscode). */
const { cases } = JSON.parse(
  readFileSync(new URL('../../../../../contracts/fixtures/sign-in-codes/cases.v1.json', import.meta.url), 'utf8'),
) as { cases: Case[] };

describe('reading what a candidate typed', () => {
  it('agrees with the control plane on every shared case', () => {
    expect(cases.length).toBeGreaterThan(15);
    for (const c of cases) {
      const read = c.kind === 'number' ? normalizeNumber(c.input) : normalizeCode(c.input);
      expect(read, c.name).toBe(c.canonical);
    }
  });

  it('has the lengths the control plane issues', () => {
    expect([NUMBER_LENGTH, CODE_LENGTH]).toEqual([8, 10]);
  });

  it('measures the input bound in bytes, as the server does', () => {
    // 16 two-byte letters are 32 bytes: read (and refused for its symbols); 17 are too long to read.
    expect(normalizeSymbols('ı'.repeat(8) + ' '.repeat(16), 8)).toBe('11111111');
    expect(normalizeSymbols('ı'.repeat(8) + ' '.repeat(17), 8)).toBeNull();
    expect(normalizeSymbols('😀'.repeat(2), 8)).toBeNull();
  });
});
