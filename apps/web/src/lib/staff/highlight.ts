/**
 * Finds the passages an AI draft quotes in the answer's transcript, exactly as the control plane's
 * validator checks quotes (services/control-plane/internal/evaluation/evaluation.go, `normalize`
 * and the evidence loop):
 * - the cited segments, in transcript order, joined by one space;
 * - Unicode NFC, then every run of whitespace (Go's unicode.IsSpace) collapsed to one space, trimmed;
 * - the first case-sensitive occurrence of the quote, normalised the same way.
 * A quote may span cited segments, adjacent or not. There is no fuzzy matching: a quote that is not
 * found is reported as not located, never guessed. `not_addressed` items quote nothing.
 */
import type { EvidenceItem, TranscriptExcerpt } from '@/lib/api/staffTypes';

/** A half-open range [start, end) of UTF-16 code units in a segment's NFC display text. */
export interface Range {
  readonly start: number;
  readonly end: number;
}

export type QuoteStatus = 'located' | 'not_located' | 'no_quote';

export interface Highlights {
  /** Merged ranges per segment id, in the segment's display text (`displayText`). */
  readonly ranges: ReadonlyMap<string, readonly Range[]>;
  /** One status per evidence item, in the same order. */
  readonly status: readonly QuoteStatus[];
}

/** The text shown for a segment: NFC, like the text the validator compares. */
export function displayText(segment: Pick<TranscriptExcerpt, 'text'>): string {
  return segment.text.normalize('NFC');
}

/** Go's unicode.IsSpace. Every such code point is in the BMP, so code units can be tested one by one. */
export function isGoSpace(code: number): boolean {
  switch (code) {
    case 0x09:
    case 0x0a:
    case 0x0b:
    case 0x0c:
    case 0x0d:
    case 0x20:
    case 0x85:
    case 0xa0:
    case 0x1680:
    case 0x2028:
    case 0x2029:
    case 0x202f:
    case 0x205f:
    case 0x3000:
      return true;
    default:
      return code >= 0x2000 && code <= 0x200a;
  }
}

interface Collapsed {
  readonly text: string;
  /** For each code unit of `text`, its index in the source (a collapsed space maps to its run's first). */
  readonly source: readonly number[];
}

/** strings.Join(strings.Fields(s), " "), keeping where each code unit came from. */
export function collapseWhitespace(input: string): Collapsed {
  let text = '';
  const source: number[] = [];
  let run = -1;
  for (let i = 0; i < input.length; i++) {
    if (isGoSpace(input.charCodeAt(i))) {
      if (run < 0) run = i;
      continue;
    }
    if (run >= 0 && text.length > 0) {
      text += ' ';
      source.push(run);
    }
    run = -1;
    text += input[i];
    source.push(i);
  }
  return { text, source };
}

/** The validator's comparison form of a text: NFC, whitespace collapsed and trimmed. */
export function normalizeForQuote(text: string): string {
  return collapseWhitespace(text.normalize('NFC')).text;
}

function mergeRanges(ranges: Range[]): Range[] {
  const sorted = [...ranges].sort((a, b) => a.start - b.start || a.end - b.end);
  const out: Range[] = [];
  for (const r of sorted) {
    const last = out[out.length - 1];
    if (last !== undefined && r.start <= last.end) {
      out[out.length - 1] = { start: last.start, end: Math.max(last.end, r.end) };
    } else {
      out.push(r);
    }
  }
  return out;
}

interface Cited {
  readonly id: string;
  /** Position in the transcript. */
  readonly index: number;
  readonly text: string;
}

export function locateQuotes(transcript: readonly TranscriptExcerpt[], evidence: readonly EvidenceItem[]): Highlights {
  const segments = new Map(transcript.map((s, index): [string, Cited] => [s.segment_id, { id: s.segment_id, index, text: displayText(s) }]));
  const found = new Map<string, Range[]>();
  const status: QuoteStatus[] = [];

  for (const item of evidence) {
    const quote = item.assessment === 'not_addressed' ? '' : normalizeForQuote(item.quote);
    if (quote === '') {
      status.push('no_quote');
      continue;
    }
    const cited = item.transcript_segment_ids.map((id) => segments.get(id));
    if (cited.length === 0 || cited.some((c) => c === undefined)) {
      status.push('not_located');
      continue;
    }
    const parts = [...new Set(cited as Cited[])].sort((a, b) => a.index - b.index);
    const haystack = collapseWhitespace(parts.map((p) => p.text).join(' '));
    const at = haystack.text.indexOf(quote);
    if (at < 0) {
      status.push('not_located');
      continue;
    }
    status.push('located');
    // The match as a range of the joined text, then its part in each segment. Only the joining spaces fall outside.
    const from = haystack.source[at] ?? 0;
    const to = (haystack.source[at + quote.length - 1] ?? from) + 1;
    let start = 0;
    for (const part of parts) {
      const lo = Math.max(from, start);
      const hi = Math.min(to, start + part.text.length);
      if (lo < hi) {
        const list = found.get(part.id) ?? [];
        list.push({ start: lo - start, end: hi - start });
        found.set(part.id, list);
      }
      start += part.text.length + 1;
    }
  }

  const ranges = new Map<string, readonly Range[]>();
  for (const [id, list] of found) ranges.set(id, mergeRanges(list));
  return { ranges, status };
}

export interface TextPart {
  readonly text: string;
  readonly highlighted: boolean;
}

/** Splits a display text into plain and highlighted parts. */
export function splitText(text: string, ranges: readonly Range[] | undefined): TextPart[] {
  if (ranges === undefined || ranges.length === 0) return text === '' ? [] : [{ text, highlighted: false }];
  const parts: TextPart[] = [];
  let at = 0;
  for (const r of ranges) {
    const start = Math.max(at, Math.min(r.start, text.length));
    const end = Math.min(r.end, text.length);
    if (start > at) parts.push({ text: text.slice(at, start), highlighted: false });
    if (end > start) parts.push({ text: text.slice(start, end), highlighted: true });
    at = Math.max(at, end);
  }
  if (at < text.length) parts.push({ text: text.slice(at), highlighted: false });
  return parts;
}
