/**
 * Captions come from LiveKit text streams on the `lk.transcription` topic. The agent publishes
 * both its own speech and the candidate's transcription there; only the interviewer's speech is
 * shown. Transcription text is rendered as plain text (React escapes it) and bounded in size.
 */

export const TRANSCRIPTION_TOPIC = 'lk.transcription';
export const ATTR_TRANSCRIBED_TRACK = 'lk.transcribed_track_id';
export const ATTR_SEGMENT_ID = 'lk.segment_id';

export const MAX_CAPTION_LINES = 100;
export const MAX_CAPTION_LENGTH = 2_000;

export interface CaptionLine {
  readonly id: string;
  readonly text: string;
}

export interface CaptionSource {
  /** Identity the stream was sent as (agents send the candidate's transcription on the candidate's behalf). */
  readonly senderIdentity: string;
  readonly localIdentity: string;
  /** `lk.transcribed_track_id`, when present. */
  readonly transcribedTrackSid: string | undefined;
  /** Every track SID this page has published (republishing after a pause creates a new SID). */
  readonly localTrackSids: ReadonlySet<string>;
}

/** True only for the interviewer's speech: never a transcription of the candidate's own audio. */
export function isInterviewerCaption(source: CaptionSource): boolean {
  if (source.senderIdentity === '' || source.senderIdentity === source.localIdentity) return false;
  const sid = source.transcribedTrackSid;
  if (sid !== undefined && sid !== '' && source.localTrackSids.has(sid)) return false;
  return true;
}

/** Appends a streamed delta, keeping the caption within MAX_CAPTION_LENGTH. */
export function appendCaptionChunk(text: string, chunk: string): string {
  if (text.length >= MAX_CAPTION_LENGTH) return text;
  return (text + chunk).slice(0, MAX_CAPTION_LENGTH);
}

/** Adds or replaces (same segment id) a finished caption line, keeping the newest MAX lines. */
export function upsertCaption(
  lines: readonly CaptionLine[],
  line: CaptionLine,
  max: number = MAX_CAPTION_LINES,
): readonly CaptionLine[] {
  const text = line.text.replace(/\s+/g, ' ').trim();
  if (text === '') return lines;
  const next = { id: line.id, text };
  const index = lines.findIndex((l) => l.id === line.id);
  if (index >= 0) {
    const copy = lines.slice();
    copy[index] = next;
    return copy;
  }
  const appended = [...lines, next];
  return appended.length > max ? appended.slice(appended.length - max) : appended;
}
