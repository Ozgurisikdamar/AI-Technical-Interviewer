import { describe, expect, it, vi } from 'vitest';
import { en } from './en';
import { DEFAULT_LOCALE, getMessages, intlLocale, isLocale, LANGUAGE_NAMES, LOCALES } from './index';
import { saveLocale } from './preference';
import { tr } from './tr';

interface Leaf {
  path: string;
  value: unknown;
}

/** Every leaf of a catalog, by its dotted path. */
function leaves(node: unknown, path: string[] = []): Leaf[] {
  if (typeof node === 'object' && node !== null && !Array.isArray(node)) {
    return Object.entries(node).flatMap(([key, value]) => leaves(value, [...path, key]));
  }
  return [{ path: path.join('.'), value: node }];
}

const byPath = (catalog: unknown) => new Map(leaves(catalog).map((leaf) => [leaf.path, leaf.value]));

/** Calls a parameterised message with sample arguments of the kind it accepts. */
function render(message: (...args: never[]) => string): string {
  const call = message as (...args: unknown[]) => string;
  const n = message.length;
  for (const sample of [3, 'X', ['A', 'B']]) {
    try {
      return call(...Array.from({ length: n }, () => sample));
    } catch {
      /* the next kind of argument */
    }
  }
  throw new Error('no sample arguments fit');
}

/** Strings that are the same in both languages on purpose. */
const SAME_IN_BOTH: ReadonlySet<string> = new Set([]);

describe('the Turkish and English catalogs', () => {
  const english = byPath(en);
  const turkish = byPath(tr);

  it('hold the same keys, and each parameterised message takes the same parameters', () => {
    expect([...turkish.keys()].sort()).toEqual([...english.keys()].sort());
    for (const [path, value] of english) {
      const other = turkish.get(path);
      expect(typeof other, path).toBe(typeof value);
      if (typeof value === 'function' && typeof other === 'function') expect(other.length, path).toBe(value.length);
    }
  });

  it('have no empty text', () => {
    for (const catalog of [english, turkish]) {
      for (const [path, value] of catalog) {
        const text = typeof value === 'function' ? render(value as (...args: never[]) => string) : value;
        expect(typeof text, path).toBe('string');
        expect((text as string).trim(), path).not.toBe('');
      }
    }
  });

  it('translate every message', () => {
    for (const [path, value] of english) {
      if (SAME_IN_BOTH.has(path)) continue;
      const other = turkish.get(path);
      const a = typeof value === 'function' ? render(value as (...args: never[]) => string) : value;
      const b = typeof other === 'function' ? render(other as (...args: never[]) => string) : other;
      expect(b, path).not.toBe(a);
    }
  });

  it('format counts in their own notation', () => {
    expect(en.staff.reports.truncated(4000)).toContain('4,000');
    expect(tr.staff.reports.truncated(4000)).toContain('4.000');
  });
});

describe('locales', () => {
  it('default to Turkish and accept only the two languages', () => {
    expect(DEFAULT_LOCALE).toBe('tr');
    expect([...LOCALES]).toEqual(['tr', 'en']);
    for (const ok of ['tr', 'en']) expect(isLocale(ok), ok).toBe(true);
    for (const bad of ['TR', 'tr-TR', 'de', '', null, undefined, 1, 'constructor']) expect(isLocale(bad), String(bad)).toBe(false);
    expect(getMessages('tr')).toBe(tr);
    expect(getMessages('en')).toBe(en);
    expect(intlLocale('tr')).toBe('tr-TR');
    expect(intlLocale('en')).toBe('en-GB');
    expect(LANGUAGE_NAMES).toEqual({ tr: 'Türkçe', en: 'English' });
  });

  it('are remembered in a cookie holding only the choice', () => {
    const doc = { cookie: '' };
    vi.stubGlobal('document', doc);
    vi.stubGlobal('window', { location: { protocol: 'http:' } });
    saveLocale('en');
    expect(doc.cookie).toBe('ati_lang=en; Path=/; Max-Age=31536000; SameSite=Lax');
    vi.stubGlobal('window', { location: { protocol: 'https:' } });
    saveLocale('tr');
    expect(doc.cookie).toBe('ati_lang=tr; Path=/; Max-Age=31536000; SameSite=Lax; Secure');
  });
});
