/**
 * Reading typed application numbers and access codes (ADR-024), exactly as the control plane does
 * (services/control-plane/internal/accesscode). Both run the shared cases in
 * contracts/fixtures/sign-in-codes, so the page refuses in the browser only what the server would
 * refuse, and never sends a malformed attempt.
 */

/** Crockford's base32: no I, L, O or U. */
const ALPHABET = '0123456789ABCDEFGHJKMNPQRSTVWXYZ';
export const NUMBER_LENGTH = 8;
export const CODE_LENGTH = 10;
/** The longest input read, in UTF-8 bytes, separators included (as the server measures it). */
const MAX_INPUT_BYTES = 32;

const SEPARATORS: ReadonlySet<string> = new Set([' ', '-', ' ']);
const READ_AS_ONE: ReadonlySet<string> = new Set(['i', 'I', 'l', 'L', 'ı', 'İ']);
const encoder = new TextEncoder();

/**
 * The canonical form of what a person typed, or null. Letter case is ignored, spaces, hyphens and
 * no-break spaces are dropped, O reads as 0 and I, L, ı and İ as 1; U is refused.
 */
export function normalizeSymbols(input: string, length: number): string | null {
  if (encoder.encode(input).length > MAX_INPUT_BYTES) return null;
  let out = '';
  for (const char of input) {
    if (SEPARATORS.has(char)) continue;
    let symbol = char;
    if (symbol === 'o' || symbol === 'O') symbol = '0';
    else if (READ_AS_ONE.has(symbol)) symbol = '1';
    if (symbol >= 'a' && symbol <= 'z') symbol = symbol.toUpperCase();
    if (symbol.length !== 1 || !ALPHABET.includes(symbol) || out.length === length) return null;
    out += symbol;
  }
  return out.length === length ? out : null;
}

export const normalizeNumber = (input: string) => normalizeSymbols(input, NUMBER_LENGTH);
export const normalizeCode = (input: string) => normalizeSymbols(input, CODE_LENGTH);
