/**
 * Pure mapping from the server's candidate view to what the UI shows and enables. The server is
 * the authority: a control is enabled only when `allowed_actions` contains its action, and every
 * state-changing request carries the observed `state_version`.
 */
import type { CandidateAction, InterviewCandidateView, InterviewState } from '@/lib/api/types';

export type Screen = 'consent' | 'setup' | 'room' | 'completed' | 'cancelled' | 'waiting';

/** The candidate's steps: the notice and choices, the setup, the interview itself. */
export const STEPS = ['notice', 'setup', 'interview'] as const;
export type Step = (typeof STEPS)[number];

/** The index in STEPS of the step a screen belongs to; null where no steps are shown. */
export function stepForScreen(screen: Screen): number | null {
  switch (screen) {
    case 'consent':
      return 0;
    case 'setup':
      return 1;
    case 'room':
      return 2;
    default:
      return null;
  }
}

const LIVE_STATES: ReadonlySet<InterviewState> = new Set(['CALIBRATION', 'QUESTION_ACTIVE', 'CANDIDATE_SPEAKING', 'PAUSED']);
const COMPLETED_STATES: ReadonlySet<InterviewState> = new Set([
  'INTERVIEW_COMPLETED',
  'EVALUATION_PENDING',
  'HUMAN_REVIEW',
  'REPORT_READY',
]);

export function screenForState(state: InterviewState): Screen {
  switch (state) {
    case 'CREATED':
    case 'CONSENT_PENDING':
      return 'consent';
    case 'READY':
      return 'setup';
    case 'CALIBRATION':
    case 'QUESTION_ACTIVE':
    case 'CANDIDATE_SPEAKING':
    case 'PAUSED':
      return 'room';
    case 'INTERVIEW_COMPLETED':
    case 'EVALUATION_PENDING':
    case 'HUMAN_REVIEW':
    case 'REPORT_READY':
      return 'completed';
    case 'CANCELLED':
      return 'cancelled';
    default:
      return 'waiting';
  }
}

export function isLiveState(state: InterviewState): boolean {
  return LIVE_STATES.has(state);
}

export function isCompletedState(state: InterviewState): boolean {
  return COMPLETED_STATES.has(state);
}

/** Live and not paused: the only states in which candidate audio may flow. */
export function isAudioIntakeState(state: InterviewState): boolean {
  return state === 'CALIBRATION' || state === 'QUESTION_ACTIVE' || state === 'CANDIDATE_SPEAKING';
}

export function isAllowed(view: InterviewCandidateView | null, action: CandidateAction): boolean {
  return view !== null && view.allowed_actions.includes(action);
}

export interface Control {
  /** Rendered in the current state. */
  readonly visible: boolean;
  /** Clickable: visible, allowed by the server, preconditions met and no request in flight. */
  readonly enabled: boolean;
}

const hidden: Control = { visible: false, enabled: false };

function control(visible: boolean, enabled: boolean): Control {
  return visible ? { visible, enabled } : hidden;
}

export interface RoomControlInput {
  /** A state-changing request is in flight. */
  readonly busy: boolean;
  /** Current text draft (text mode). */
  readonly draft?: string;
  readonly maxTextLength?: number;
}

export interface RoomControls {
  readonly begin: Control;
  readonly pause: Control;
  readonly resume: Control;
  readonly finishAnswer: Control;
  /** True while a completion request is pending: the button reads "Finishing…". */
  readonly finishing: boolean;
  readonly withdraw: Control;
  readonly textInput: Control;
  readonly textSend: Control;
}

export function roomControls(view: InterviewCandidateView, input: RoomControlInput): RoomControls {
  const { state } = view;
  const idle = !input.busy;
  const answering = state === 'QUESTION_ACTIVE' || state === 'CANDIDATE_SPEAKING';
  const hasPrompt = view.active_prompt !== null;
  const textMode = view.input_mode === 'text';
  const draft = input.draft ?? '';
  const max = input.maxTextLength ?? 4_000;
  const draftOk = draft.trim().length > 0 && draft.length <= max;

  return {
    begin: control(state === 'CALIBRATION', idle && isAllowed(view, 'begin')),
    pause: control(isAudioIntakeState(state), idle && isAllowed(view, 'pause')),
    resume: control(state === 'PAUSED', idle && isAllowed(view, 'resume')),
    finishAnswer: control(
      answering,
      idle && hasPrompt && !view.pending_completion && isAllowed(view, 'finish_answer'),
    ),
    finishing: answering && view.pending_completion,
    withdraw: control(isLiveState(state), idle && isAllowed(view, 'withdraw')),
    textInput: control(textMode && answering, hasPrompt && !view.pending_completion && isAllowed(view, 'text_response')),
    textSend: control(
      textMode && answering,
      idle && hasPrompt && draftOk && !view.pending_completion && isAllowed(view, 'text_response'),
    ),
  };
}

export interface SetupControlInput {
  readonly busy: boolean;
  /** The microphone check succeeded in this page. */
  readonly micReady: boolean;
  /** Voice cannot be used: purpose declined, microphone failed or the server refused voice. */
  readonly voiceUnavailable: boolean;
}

export interface SetupControls {
  readonly checkMic: Control;
  readonly startVoice: Control;
  readonly startText: Control;
  readonly withdraw: Control;
}

export function setupControls(view: InterviewCandidateView, input: SetupControlInput): SetupControls {
  const ready = view.state === 'READY';
  const canStart = ready && !input.busy && isAllowed(view, 'start');
  return {
    checkMic: control(ready && !input.voiceUnavailable, !input.busy),
    startVoice: control(ready && !input.voiceUnavailable, canStart && input.micReady),
    startText: control(ready, canStart),
    withdraw: control(ready, !input.busy && isAllowed(view, 'withdraw')),
  };
}

export function consentSubmitEnabled(view: InterviewCandidateView, busy: boolean): boolean {
  return !busy && isAllowed(view, 'consent');
}

export type ConnectionStatus = 'idle' | 'connecting' | 'connected' | 'reconnecting' | 'disconnected' | 'failed';

export type RoomStatus = 'connecting' | 'reconnecting' | 'disconnected' | 'paused' | 'interviewer_speaking' | 'listening';

export function roomStatus(input: {
  readonly state: InterviewState;
  readonly connection: ConnectionStatus;
  readonly agentSpeaking: boolean;
}): RoomStatus {
  if (input.connection === 'reconnecting' || input.connection === 'disconnected') return 'reconnecting';
  if (input.connection === 'failed') return 'disconnected';
  if (input.state === 'PAUSED') return 'paused';
  if (input.connection !== 'connected') return 'connecting';
  return input.agentSpeaking ? 'interviewer_speaking' : 'listening';
}

export interface RealtimeDesire {
  /** Be in (or join) the LiveKit room. */
  readonly connect: boolean;
  /** Publish the microphone. */
  readonly microphone: boolean;
}

/**
 * Joining needs `join_media`; once joined, the room is kept while the interview is live
 * (including PAUSED, with the microphone unpublished) and, after completion, until the server
 * closes it so closing remarks can still be heard. The microphone is published only in voice
 * mode and only while audio intake is open.
 */
export function realtimeDesire(view: InterviewCandidateView | null, connection: ConnectionStatus): RealtimeDesire {
  if (view === null) return { connect: false, microphone: false };
  const joined = connection === 'connecting' || connection === 'connected' || connection === 'reconnecting';
  const live = isLiveState(view.state);
  const connect = live ? joined || isAllowed(view, 'join_media') : isCompletedState(view.state) && joined;
  const microphone = connect && view.input_mode === 'voice' && isAudioIntakeState(view.state);
  return { connect, microphone };
}

/**
 * Responses can arrive out of order (a slow poll after a newer action response). A view replaces
 * the current one if its state_version is newer, or equal and its request was issued later.
 */
export function shouldApplyView(
  current: { readonly view: InterviewCandidateView | null; readonly seq: number },
  incoming: InterviewCandidateView,
  incomingSeq: number,
): boolean {
  if (current.view === null) return true;
  if (incoming.id !== current.view.id) return false;
  if (incoming.state_version !== current.view.state_version) {
    return incoming.state_version > current.view.state_version;
  }
  return incomingSeq > current.seq;
}

/** Interview progress for display, clamped to the contract's bounds. */
export function progressLabelValues(view: InterviewCandidateView): { readonly done: number; readonly total: number } {
  const total = Math.max(1, view.progress.total_items);
  const done = Math.min(Math.max(0, view.progress.completed_items), total);
  return { done, total };
}
