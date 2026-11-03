import { describe, expect, it } from 'vitest';
import { MAX_CAPTION_LENGTH, appendCaptionChunk, isInterviewerCaption, upsertCaption } from './captions';

const local = new Set(['TR_cand_old', 'TR_cand_now']);

describe('isInterviewerCaption (interviewer speech only)', () => {
  it('shows the agent transcribing its own audio track', () => {
    expect(
      isInterviewerCaption({ senderIdentity: 'agent-1', localIdentity: 'candidate', transcribedTrackSid: 'TR_agent', localTrackSids: local }),
    ).toBe(true);
  });

  it('hides transcriptions sent on the candidate’s behalf', () => {
    expect(
      isInterviewerCaption({ senderIdentity: 'candidate', localIdentity: 'candidate', transcribedTrackSid: 'TR_agent', localTrackSids: local }),
    ).toBe(false);
  });

  it('hides transcriptions of any microphone track this page published, including before a pause', () => {
    for (const sid of local) {
      expect(
        isInterviewerCaption({ senderIdentity: 'agent-1', localIdentity: 'candidate', transcribedTrackSid: sid, localTrackSids: local }),
      ).toBe(false);
    }
  });

  it('shows agent text without a track attribute', () => {
    expect(
      isInterviewerCaption({ senderIdentity: 'agent-1', localIdentity: 'candidate', transcribedTrackSid: undefined, localTrackSids: local }),
    ).toBe(true);
  });

  it('hides streams with no sender', () => {
    expect(isInterviewerCaption({ senderIdentity: '', localIdentity: 'candidate', transcribedTrackSid: undefined, localTrackSids: local })).toBe(
      false,
    );
  });
});

describe('caption buffers', () => {
  it('appends deltas within the length bound', () => {
    expect(appendCaptionChunk('Hello', ', world')).toBe('Hello, world');
    const long = appendCaptionChunk('a'.repeat(MAX_CAPTION_LENGTH - 2), 'bcdef');
    expect(long).toHaveLength(MAX_CAPTION_LENGTH);
    expect(appendCaptionChunk(long, 'more')).toBe(long);
  });

  it('replaces a line with the same segment id instead of duplicating it', () => {
    const lines = upsertCaption(upsertCaption([], { id: 's1', text: 'Hel' }), { id: 's1', text: 'Hello there' });
    expect(lines).toEqual([{ id: 's1', text: 'Hello there' }]);
  });

  it('normalises whitespace and skips empty lines', () => {
    expect(upsertCaption([], { id: 's1', text: '  Hello \n  there ' })).toEqual([{ id: 's1', text: 'Hello there' }]);
    expect(upsertCaption([], { id: 's2', text: '   ' })).toEqual([]);
  });

  it('keeps only the newest lines', () => {
    let lines: ReturnType<typeof upsertCaption> = [];
    for (let i = 0; i < 5; i++) lines = upsertCaption(lines, { id: `s${i}`, text: `line ${i}` }, 3);
    expect(lines.map((l) => l.id)).toEqual(['s2', 's3', 's4']);
  });
});
