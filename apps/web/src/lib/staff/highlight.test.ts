import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import type { EvidenceItem, TranscriptExcerpt } from '@/lib/api/staffTypes';
import { collapseWhitespace, displayText, isGoSpace, locateQuotes, normalizeForQuote, splitText } from './highlight';

interface SharedCase {
  name: string;
  segments: { id: string; text: string }[];
  cited: string[];
  quote: string;
  found: boolean;
}

/** The cases the control plane's quote rule is tested with (internal/evaluation/quote_internal_test.go). */
const shared = JSON.parse(
  readFileSync(new URL('../../../../../contracts/fixtures/quote-matching/cases.v1.json', import.meta.url), 'utf8'),
) as { cases: SharedCase[] };

const segment = (segment_id: string, text: string, start_ms = 0): TranscriptExcerpt => ({
  segment_id,
  start_ms,
  end_ms: start_ms + 1_000,
  text,
});

const item = (ids: string[], quote: string, assessment: EvidenceItem['assessment'] = 'correct'): EvidenceItem => ({
  dimension: 'technical_accuracy',
  transcript_start_ms: 0,
  transcript_end_ms: 1_000,
  transcript_segment_ids: ids,
  quote,
  claim: 'c',
  assessment,
  reference_ids: [],
});

function highlighted(transcript: TranscriptExcerpt[], evidence: EvidenceItem[]): string[] {
  const { ranges } = locateQuotes(transcript, evidence);
  return transcript.flatMap((s) =>
    splitText(displayText(s), ranges.get(s.segment_id))
      .filter((p) => p.highlighted)
      .map((p) => p.text),
  );
}

describe('locateQuotes agrees with the validator', () => {
  it.each(shared.cases.map((c) => [c.name, c] as const))('%s', (_name, c) => {
    const transcript = c.segments.map((s, i) => segment(s.id, s.text, i * 1_000));
    const { status } = locateQuotes(transcript, [item(c.cited, c.quote)]);
    expect(status).toEqual([c.found ? 'located' : 'not_located']);
  });

  it('runs every shared case', () => {
    expect(shared.cases.length).toBeGreaterThanOrEqual(20);
  });
});

describe('locateQuotes', () => {
  const a = 'seg_a000000000000001';
  const b = 'seg_a000000000000002';
  const c = 'seg_a000000000000003';

  it('highlights the first occurrence only', () => {
    const t = [segment(a, 'retry the request; retry the request again')];
    const { ranges } = locateQuotes(t, [item([a], 'retry the request')]);
    expect(ranges.get(a)).toEqual([{ start: 0, end: 17 }]);
  });

  it('maps collapsed whitespace back to the displayed text', () => {
    const t = [segment(a, 'dead  rows\n\tcause   bloat today')];
    expect(highlighted(t, [item([a], 'dead rows cause bloat')])).toEqual(['dead  rows\n\tcause   bloat']);
  });

  it('splits a quote across segments and leaves the joining space out', () => {
    const t = [segment(a, 'Old versions stay'), segment(b, 'um'), segment(c, 'until VACUUM runs.')];
    const { ranges } = locateQuotes(t, [item([c, a], 'stay until VACUUM')]);
    expect(ranges.get(a)).toEqual([{ start: 13, end: 17 }]);
    expect(ranges.get(b)).toBeUndefined();
    expect(ranges.get(c)).toEqual([{ start: 0, end: 12 }]);
  });

  it('merges overlapping quotes of several evidence items', () => {
    const t = [segment(a, 'keys are stored with the response')];
    const { ranges, status } = locateQuotes(t, [item([a], 'keys are stored'), item([a], 'stored with'), item([a], 'response')]);
    expect(status).toEqual(['located', 'located', 'located']);
    // 'keys are stored' [0, 15) and 'stored with' [9, 20) overlap; 'response' [25, 33) does not.
    expect(ranges.get(a)).toEqual([
      { start: 0, end: 20 },
      { start: 25, end: 33 },
    ]);
  });

  it('highlights in NFC display text when the quote is decomposed', () => {
    const t = [segment(a, 'yeni bir satır sürümü yazar')];
    expect(highlighted(t, [item([a], 'satır sürümü')])).toEqual(['satır sürümü']);
  });

  it('keeps characters outside the BMP whole', () => {
    const t = [segment(a, 'p99 latency 📈 doubled')];
    expect(highlighted(t, [item([a], '📈 doubled')])).toEqual(['📈 doubled']);
  });

  it('quotes nothing for not_addressed items and empty quotes', () => {
    const t = [segment(a, 'anything')];
    const { ranges, status } = locateQuotes(t, [item([a], '', 'not_addressed'), item([a], '   ')]);
    expect(status).toEqual(['no_quote', 'no_quote']);
    expect(ranges.size).toBe(0);
  });

  it('reports a quote citing an unknown segment, or none, as not located', () => {
    const t = [segment(a, 'anything')];
    const { status } = locateQuotes(t, [item(['seg_unknown00000000001'], 'anything'), item([], 'anything')]);
    expect(status).toEqual(['not_located', 'not_located']);
  });

  it('never matches loosely', () => {
    const t = [segment(a, 'Each update writes a new row version.')];
    const { status, ranges } = locateQuotes(t, [item([a], 'each update'), item([a], 'Each  update  writes a new  row  versions')]);
    expect(status).toEqual(['not_located', 'not_located']);
    expect(ranges.size).toBe(0);
  });
});

describe('whitespace and normalisation helpers', () => {
  it('uses the Go unicode.IsSpace set', () => {
    for (const code of [0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x20, 0x85, 0xa0, 0x1680, 0x2000, 0x200a, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000]) {
      expect(isGoSpace(code), code.toString(16)).toBe(true);
    }
    for (const code of [0x200b, 0xfeff, 0x180e, 0x61, 0x2e]) {
      expect(isGoSpace(code), code.toString(16)).toBe(false);
    }
  });

  it('collapses and trims like strings.Fields', () => {
    expect(collapseWhitespace('  a   b\n').text).toBe('a b');
    expect(collapseWhitespace('  a   b\n').source).toEqual([2, 3, 6]);
    expect(normalizeForQuote('　é\t')).toBe('é');
  });

  it('splits text into plain and highlighted parts', () => {
    expect(splitText('abcdef', [{ start: 1, end: 3 }, { start: 4, end: 9 }])).toEqual([
      { text: 'a', highlighted: false },
      { text: 'bc', highlighted: true },
      { text: 'd', highlighted: false },
      { text: 'ef', highlighted: true },
    ]);
    expect(splitText('abc', undefined)).toEqual([{ text: 'abc', highlighted: false }]);
    expect(splitText('', [])).toEqual([]);
  });
});
