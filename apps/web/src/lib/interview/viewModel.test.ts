import { describe, expect, it } from 'vitest';
import { INTERVIEW_STATES, type CandidateAction, type InterviewCandidateView, type InterviewState } from '@/lib/api/types';
import {
  consentSubmitEnabled,
  isAudioIntakeState,
  progressLabelValues,
  realtimeDesire,
  roomControls,
  roomStatus,
  screenForState,
  setupControls,
  shouldApplyView,
  STEPS,
  stepForScreen,
} from './viewModel';

const ACT = 'act_ABCDEFGHIJKLMNOPQRSTUV';

function view(state: InterviewState, actions: CandidateAction[], patch: Partial<InterviewCandidateView> = {}): InterviewCandidateView {
  return {
    id: 'ses_ABCDEFGHIJKLMNOPQRSTUV',
    state,
    state_version: 5,
    progress: { completed_items: 1, total_items: 3 },
    input_mode: 'voice',
    active_prompt: { activation_id: ACT, kind: 'question', text: 'Q' },
    paused_from: null,
    pending_completion: false,
    recording_active: false,
    allowed_actions: actions,
    environment: 'dev',
    application_number: null,
    interview_title: null,
    language: null,
    ...patch,
  };
}

describe('screenForState', () => {
  it.each<[InterviewState, string]>([
    ['CREATED', 'consent'],
    ['CONSENT_PENDING', 'consent'],
    ['READY', 'setup'],
    ['CALIBRATION', 'room'],
    ['QUESTION_ACTIVE', 'room'],
    ['CANDIDATE_SPEAKING', 'room'],
    ['PAUSED', 'room'],
    ['INTERVIEW_COMPLETED', 'completed'],
    ['EVALUATION_PENDING', 'completed'],
    ['HUMAN_REVIEW', 'completed'],
    ['REPORT_READY', 'completed'],
    ['CANCELLED', 'cancelled'],
    ['UNKNOWN', 'waiting'],
  ])('%s -> %s', (state, screen) => {
    expect(screenForState(state)).toBe(screen);
  });

  it('covers every state of the contract', () => {
    for (const state of INTERVIEW_STATES) expect(screenForState(state)).not.toBe('waiting');
  });
});

describe('room controls are gated by allowed_actions', () => {
  it('enables nothing the server did not allow', () => {
    const c = roomControls(view('QUESTION_ACTIVE', []), { busy: false });
    expect(c.pause).toEqual({ visible: true, enabled: false });
    expect(c.finishAnswer).toEqual({ visible: true, enabled: false });
    expect(c.withdraw).toEqual({ visible: true, enabled: false });
  });

  it('enables pause, finish answer and withdraw when allowed while answering', () => {
    const c = roomControls(view('CANDIDATE_SPEAKING', ['pause', 'finish_answer', 'withdraw']), { busy: false });
    expect(c.pause.enabled).toBe(true);
    expect(c.finishAnswer.enabled).toBe(true);
    expect(c.withdraw.enabled).toBe(true);
    expect(c.begin.visible).toBe(false);
    expect(c.resume.visible).toBe(false);
  });

  it('shows Begin only in calibration', () => {
    expect(roomControls(view('CALIBRATION', ['begin', 'pause']), { busy: false }).begin).toEqual({ visible: true, enabled: true });
    expect(roomControls(view('QUESTION_ACTIVE', ['begin']), { busy: false }).begin.visible).toBe(false);
    expect(roomControls(view('CALIBRATION', ['pause']), { busy: false }).begin.enabled).toBe(false);
  });

  it('shows Resume (not Pause) while paused', () => {
    const c = roomControls(view('PAUSED', ['resume', 'withdraw', 'pause']), { busy: false });
    expect(c.resume).toEqual({ visible: true, enabled: true });
    expect(c.pause.visible).toBe(false);
    expect(c.finishAnswer.visible).toBe(false);
  });

  it('disables Finish answer while a completion is pending and reports "finishing"', () => {
    const c = roomControls(view('CANDIDATE_SPEAKING', ['finish_answer'], { pending_completion: true }), { busy: false });
    expect(c.finishAnswer.enabled).toBe(false);
    expect(c.finishing).toBe(true);
  });

  it('disables Finish answer without an open prompt', () => {
    const c = roomControls(view('QUESTION_ACTIVE', ['finish_answer'], { active_prompt: null }), { busy: false });
    expect(c.finishAnswer.enabled).toBe(false);
  });

  it('disables every action while a request is in flight', () => {
    const c = roomControls(view('QUESTION_ACTIVE', ['pause', 'finish_answer', 'withdraw']), { busy: true });
    expect([c.pause.enabled, c.finishAnswer.enabled, c.withdraw.enabled]).toEqual([false, false, false]);
  });

  it('offers the text box only in text mode and only with text_response allowed and a non-empty draft', () => {
    const textView = view('QUESTION_ACTIVE', ['text_response', 'finish_answer'], { input_mode: 'text' });
    expect(roomControls(textView, { busy: false, draft: '' }).textSend.enabled).toBe(false);
    expect(roomControls(textView, { busy: false, draft: '   ' }).textSend.enabled).toBe(false);
    expect(roomControls(textView, { busy: false, draft: 'An answer' }).textSend.enabled).toBe(true);
    expect(roomControls(textView, { busy: false, draft: 'x'.repeat(4_001) }).textSend.enabled).toBe(false);
    expect(
      roomControls(view('QUESTION_ACTIVE', ['finish_answer'], { input_mode: 'text' }), { busy: false, draft: 'A' }).textSend
        .enabled,
    ).toBe(false);
    expect(roomControls(view('QUESTION_ACTIVE', ['text_response']), { busy: false, draft: 'A' }).textInput.visible).toBe(false);
  });
});

describe('setup and consent gating', () => {
  it('starts voice only after a successful microphone check and when start is allowed', () => {
    const ready = view('READY', ['start', 'withdraw'], { input_mode: null, active_prompt: null });
    expect(setupControls(ready, { busy: false, micReady: false, voiceUnavailable: false }).startVoice.enabled).toBe(false);
    expect(setupControls(ready, { busy: false, micReady: true, voiceUnavailable: false }).startVoice.enabled).toBe(true);
    expect(setupControls(ready, { busy: false, micReady: false, voiceUnavailable: false }).startText.enabled).toBe(true);
    const noStart = view('READY', ['withdraw'], { input_mode: null });
    expect(setupControls(noStart, { busy: false, micReady: true, voiceUnavailable: false }).startVoice.enabled).toBe(false);
    expect(setupControls(noStart, { busy: false, micReady: true, voiceUnavailable: false }).startText.enabled).toBe(false);
  });

  it('hides voice when it is unavailable but keeps text', () => {
    const ready = view('READY', ['start'], { input_mode: null });
    const c = setupControls(ready, { busy: false, micReady: true, voiceUnavailable: true });
    expect(c.startVoice.visible).toBe(false);
    expect(c.startText).toEqual({ visible: true, enabled: true });
  });

  it('enables consent submission only when allowed', () => {
    expect(consentSubmitEnabled(view('CONSENT_PENDING', ['consent']), false)).toBe(true);
    expect(consentSubmitEnabled(view('CONSENT_PENDING', []), false)).toBe(false);
    expect(consentSubmitEnabled(view('CONSENT_PENDING', ['consent']), true)).toBe(false);
  });
});

describe('roomStatus', () => {
  it.each([
    [{ state: 'QUESTION_ACTIVE', connection: 'connected', agentSpeaking: true }, 'interviewer_speaking'],
    [{ state: 'QUESTION_ACTIVE', connection: 'connected', agentSpeaking: false }, 'listening'],
    [{ state: 'PAUSED', connection: 'connected', agentSpeaking: true }, 'paused'],
    [{ state: 'PAUSED', connection: 'idle', agentSpeaking: false }, 'paused'],
    [{ state: 'QUESTION_ACTIVE', connection: 'reconnecting', agentSpeaking: false }, 'reconnecting'],
    [{ state: 'PAUSED', connection: 'disconnected', agentSpeaking: false }, 'reconnecting'],
    [{ state: 'CALIBRATION', connection: 'connecting', agentSpeaking: false }, 'connecting'],
    [{ state: 'CALIBRATION', connection: 'failed', agentSpeaking: false }, 'disconnected'],
  ] as const)('%o -> %s', (input, expected) => {
    expect(roomStatus(input)).toBe(expected);
  });
});

describe('realtimeDesire (media follows the server state)', () => {
  it('joins only when join_media is allowed', () => {
    expect(realtimeDesire(view('CALIBRATION', ['join_media']), 'idle').connect).toBe(true);
    expect(realtimeDesire(view('CALIBRATION', []), 'idle').connect).toBe(false);
  });

  it('stays in the room while paused but never publishes the microphone', () => {
    const paused = realtimeDesire(view('PAUSED', ['resume']), 'connected');
    expect(paused).toEqual({ connect: true, microphone: false });
  });

  it('publishes the microphone only in voice mode during audio intake', () => {
    expect(realtimeDesire(view('QUESTION_ACTIVE', ['join_media']), 'idle').microphone).toBe(true);
    expect(realtimeDesire(view('QUESTION_ACTIVE', ['join_media'], { input_mode: 'text' }), 'connected').microphone).toBe(false);
    expect(isAudioIntakeState('PAUSED')).toBe(false);
  });

  it('keeps an open room after completion for closing remarks, with the microphone off, but never rejoins', () => {
    expect(realtimeDesire(view('INTERVIEW_COMPLETED', []), 'connected')).toEqual({ connect: true, microphone: false });
    expect(realtimeDesire(view('INTERVIEW_COMPLETED', ['join_media']), 'idle').connect).toBe(false);
  });

  it('leaves on cancellation and before the interview starts', () => {
    expect(realtimeDesire(view('CANCELLED', ['join_media']), 'connected').connect).toBe(false);
    expect(realtimeDesire(view('READY', ['join_media']), 'idle').connect).toBe(false);
    expect(realtimeDesire(null, 'connected')).toEqual({ connect: false, microphone: false });
  });
});

describe('shouldApplyView', () => {
  const v5 = view('QUESTION_ACTIVE', []);
  it('accepts newer state versions and rejects older ones regardless of arrival order', () => {
    expect(shouldApplyView({ view: v5, seq: 10 }, { ...v5, state_version: 6 }, 1)).toBe(true);
    expect(shouldApplyView({ view: v5, seq: 1 }, { ...v5, state_version: 4 }, 99)).toBe(false);
  });

  it('at equal versions, prefers the later-issued request', () => {
    expect(shouldApplyView({ view: v5, seq: 3 }, { ...v5, pending_completion: true }, 4)).toBe(true);
    expect(shouldApplyView({ view: v5, seq: 4 }, { ...v5, pending_completion: false }, 3)).toBe(false);
  });

  it('never mixes interviews', () => {
    expect(shouldApplyView({ view: v5, seq: 1 }, { ...v5, id: 'ses_ZZZZZZZZZZZZZZZZZZZZZZ', state_version: 9 }, 2)).toBe(false);
  });

  it('accepts the first view', () => {
    expect(shouldApplyView({ view: null, seq: 0 }, v5, 1)).toBe(true);
  });
});

describe('progressLabelValues', () => {
  it('clamps to the contract bounds', () => {
    expect(progressLabelValues(view('QUESTION_ACTIVE', [], { progress: { completed_items: 9, total_items: 3 } }))).toEqual({
      done: 3,
      total: 3,
    });
  });
});

describe('steps', () => {
  it('follow the screens, and none is shown before or after the interview', () => {
    expect([...STEPS]).toEqual(['notice', 'setup', 'interview']);
    expect(stepForScreen(screenForState('CONSENT_PENDING'))).toBe(0);
    expect(stepForScreen(screenForState('CREATED'))).toBe(0);
    expect(stepForScreen(screenForState('READY'))).toBe(1);
    for (const state of ['CALIBRATION', 'QUESTION_ACTIVE', 'CANDIDATE_SPEAKING', 'PAUSED'] as const) {
      expect(stepForScreen(screenForState(state)), state).toBe(2);
    }
    for (const state of ['INTERVIEW_COMPLETED', 'REPORT_READY', 'CANCELLED', 'UNKNOWN'] as const) {
      expect(stepForScreen(screenForState(state)), state).toBeNull();
    }
  });
});
