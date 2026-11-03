/**
 * Candidate side of the LiveKit room, independent of React.
 *
 * The UI declares what it wants (be in the room; publish the microphone) and a single serial
 * reconcile loop makes it so. Every operation runs one at a time, so a pause that arrives while
 * the microphone is starting still ends with the microphone unpublished.
 *
 * Security properties (ADR-016, ADR-018, threat-model addendum):
 * - nothing is ever published on the data channel (the candidate token cannot publish data, and
 *   this module has no call that would); text answers use the HTTP API only;
 * - the microphone is the only track ever published, only in voice mode while audio intake is open;
 * - the LiveKit token is used for the join and then dropped (never stored or logged).
 */
import type {
  DisconnectReason,
  LocalAudioTrack,
  LocalTrackPublication,
  RemoteParticipant,
  RemoteTrack,
  RemoteTrackPublication,
  Room,
  RoomEvent,
  TextStreamReader,
} from 'livekit-client';
import type * as LiveKitModule from 'livekit-client';
import type { RealtimeSession } from '@/lib/api/types';
import { AUDIO_CONSTRAINTS, explainMediaError, type MediaErrorKind } from '@/lib/audio/mediaErrors';
import type { ConnectionStatus } from '@/lib/interview/viewModel';
import {
  ATTR_SEGMENT_ID,
  ATTR_TRANSCRIBED_TRACK,
  TRANSCRIPTION_TOPIC,
  appendCaptionChunk,
  isInterviewerCaption,
  upsertCaption,
  type CaptionLine,
} from './captions';

type LiveKit = typeof LiveKitModule;

export type RealtimeFailure = 'connect_failed' | 'opened_elsewhere' | 'unsupported_browser' | null;
export type MicStatus = 'off' | 'starting' | 'on' | 'error';

export interface RealtimeSnapshot {
  readonly connection: ConnectionStatus;
  readonly failure: RealtimeFailure;
  readonly agentSpeaking: boolean;
  readonly canPlaybackAudio: boolean;
  readonly mic: MicStatus;
  readonly micError: MediaErrorKind | null;
  readonly captions: readonly CaptionLine[];
  readonly interimCaption: CaptionLine | null;
}

export const INITIAL_REALTIME_SNAPSHOT: RealtimeSnapshot = {
  connection: 'idle',
  failure: null,
  agentSpeaking: false,
  canPlaybackAudio: true,
  mic: 'off',
  micError: null,
  captions: [],
  interimCaption: null,
};

export interface RealtimeDeps {
  readonly loadLiveKit: () => Promise<LiveKit>;
  readonly requestSession: () => Promise<RealtimeSession>;
}

export interface RealtimeCallbacks {
  /** The microphone ended or could not be started while audio intake was open. */
  readonly onMicrophoneLost: (kind: MediaErrorKind) => void;
  /** The room closed without the page asking (the interview state may have changed). */
  readonly onUnexpectedDisconnect: () => void;
}

const NO_CALLBACKS: RealtimeCallbacks = { onMicrophoneLost: () => undefined, onUnexpectedDisconnect: () => undefined };

export const MAX_CONNECT_ATTEMPTS = 6;

/** Backoff before connection attempt `attempt` (1-based): 1 s, 2 s, 4 s, 8 s, then 15 s. */
export function reconnectDelayMs(attempt: number): number {
  const n = Math.max(1, Math.floor(attempt));
  return Math.min(15_000, 1_000 * 2 ** (n - 1));
}

const AGENT_STATE_ATTRIBUTE = 'lk.agent.state';

export class RealtimeController {
  private snapshot: RealtimeSnapshot = INITIAL_REALTIME_SNAPSHOT;
  private readonly listeners = new Set<() => void>();
  private desired = { connect: false, microphone: false };
  private active = false;

  private callbacks: RealtimeCallbacks = NO_CALLBACKS;
  private audioElement: HTMLAudioElement | null = null;
  /** The interviewer's audio track currently played through `audioElement`. */
  private interviewerAudio: RemoteTrack | null = null;

  private lk: LiveKit | null = null;
  private room: Room | null = null;
  private roomBindings: Array<[RoomEvent, (...args: never[]) => void]> = [];
  private canPublishAudio = false;
  private micTrack: LocalAudioTrack | null = null;
  /** Set when the microphone failed during intake; cleared when intake closes (pause) or on retry. */
  private micBlocked = false;
  private everConnected = false;

  private running = false;
  private rerun = false;
  private retryTimer: ReturnType<typeof setTimeout> | null = null;
  private attempts = 0;

  /** Every local track SID published from this page (to keep the candidate's own words out of captions). */
  private readonly localTrackSids = new Set<string>();

  constructor(private readonly deps: RealtimeDeps) {}

  readonly subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };

  readonly getSnapshot = (): RealtimeSnapshot => this.snapshot;

  setCallbacks(callbacks: RealtimeCallbacks): void {
    this.callbacks = callbacks;
  }

  /** Callback ref for the <audio> element that plays the interviewer. */
  readonly setAudioElement = (element: HTMLAudioElement | null): void => {
    if (element === this.audioElement) return;
    if (this.audioElement && this.interviewerAudio) this.interviewerAudio.detach(this.audioElement);
    this.audioElement = element;
    if (element && this.interviewerAudio) this.interviewerAudio.attach(element);
  };

  /** Called when the owning component mounts (and again after a Strict Mode remount). */
  activate(): void {
    this.active = true;
    this.kick();
  }

  /** Leaves the room and stops the microphone; the controller can be activated again. */
  deactivate(): void {
    this.active = false;
    this.clearRetry();
    this.kick();
  }

  setDesired(connect: boolean, microphone: boolean): void {
    if (!microphone) this.micBlocked = false;
    if (!connect) {
      this.clearRetry();
      this.attempts = 0;
    }
    if (connect === this.desired.connect && microphone === this.desired.microphone) return;
    this.desired = { connect, microphone };
    this.kick();
  }

  /** Manual retry after a connection failure, or "use this window" after a duplicate join. */
  retry(): void {
    this.attempts = 0;
    this.clearRetry();
    this.update({ failure: null, connection: this.room ? this.snapshot.connection : 'idle' });
    this.kick();
  }

  retryMicrophone(): void {
    this.micBlocked = false;
    this.update({ micError: null, mic: this.micTrack ? 'on' : 'off' });
    this.kick();
  }

  /** Must be called from a click handler: browsers only allow playback after a user gesture. */
  startAudio(): Promise<void> {
    const room = this.room;
    if (!room) return Promise.resolve();
    return room
      .startAudio()
      .catch(() => undefined)
      .then(() => {
        if (room === this.room) this.update({ canPlaybackAudio: room.canPlaybackAudio });
      });
  }

  // ---------------------------------------------------------------------------------------------
  // Reconcile loop

  private wantRoom(): boolean {
    return this.active && this.desired.connect;
  }

  private kick(): void {
    void this.reconcile();
  }

  private async reconcile(): Promise<void> {
    if (this.running) {
      this.rerun = true;
      return;
    }
    this.running = true;
    try {
      do {
        this.rerun = false;
        await this.step();
      } while (this.rerun);
    } catch {
      // Each step handles its own failures; never let the loop reject unhandled.
    } finally {
      this.running = false;
    }
  }

  private async step(): Promise<void> {
    if (!this.wantRoom()) {
      if (this.room) await this.leave();
      return;
    }
    if (!this.room) {
      if (this.retryTimer !== null || this.snapshot.failure !== null) return;
      await this.join();
      // Re-evaluate from the top: wishes may have changed while joining.
      this.rerun = this.room !== null;
      return;
    }
    const connection = this.snapshot.connection;
    const wantMic = this.desired.microphone && this.canPublishAudio && !this.micBlocked;
    if (!wantMic && this.micTrack && (connection === 'connected' || connection === 'reconnecting')) {
      await this.disableMicrophone();
    } else if (wantMic && !this.micTrack && connection === 'connected') {
      await this.enableMicrophone();
    }
  }

  private async join(): Promise<void> {
    this.update({ connection: 'connecting' });
    let lk: LiveKit;
    try {
      lk = this.lk ?? (this.lk = await this.deps.loadLiveKit());
    } catch {
      this.scheduleRetry();
      return;
    }
    lk.setLogLevel(lk.LogLevel.warn);
    if (!lk.isBrowserSupported()) {
      this.update({ connection: 'failed', failure: 'unsupported_browser' });
      return;
    }

    let session: RealtimeSession;
    try {
      session = await this.deps.requestSession();
    } catch {
      this.scheduleRetry();
      return;
    }
    if (!this.wantRoom()) {
      this.update({ connection: 'idle' });
      return;
    }

    const room = new lk.Room({
      adaptiveStream: false,
      dynacast: false,
      stopLocalTrackOnUnpublish: true,
      disconnectOnPageLeave: true,
      audioCaptureDefaults: AUDIO_CONSTRAINTS,
    });
    this.bind(lk, room);
    this.room = room;
    this.canPublishAudio = session.can_publish_audio;
    try {
      await room.connect(session.livekit_url, session.token, { autoSubscribe: true });
    } catch {
      if (this.room === room) {
        this.room = null;
        this.canPublishAudio = false;
      }
      this.unbind(room);
      await room.disconnect().catch(() => undefined);
      this.scheduleRetry();
      return;
    }
    if (this.room !== room) return;
    this.attempts = 0;
    this.everConnected = true;
    this.update({ connection: 'connected', failure: null, canPlaybackAudio: room.canPlaybackAudio });
    this.refreshAgentState(room);
  }

  private async leave(): Promise<void> {
    const room = this.room;
    if (!room) return;
    this.room = null;
    this.canPublishAudio = false;
    const track = this.releaseMicTrackHandle();
    this.unbind(room);
    try {
      await room.disconnect(true);
    } catch {
      // Already gone.
    }
    // disconnect(true) stops local tracks; stopping explicitly guarantees the capture device is released.
    track?.stop();
    this.detachAudio();
    this.update({
      connection: 'idle',
      agentSpeaking: false,
      mic: this.snapshot.mic === 'error' ? 'error' : 'off',
      interimCaption: null,
      canPlaybackAudio: true,
    });
  }

  private scheduleRetry(minDelayMs = 0): void {
    this.attempts += 1;
    if (this.attempts > MAX_CONNECT_ATTEMPTS) {
      this.update({ connection: 'failed', failure: 'connect_failed' });
      return;
    }
    this.update({ connection: this.everConnected ? 'disconnected' : 'connecting' });
    this.clearRetry();
    this.retryTimer = setTimeout(
      () => {
        this.retryTimer = null;
        this.kick();
      },
      Math.max(minDelayMs, reconnectDelayMs(this.attempts)),
    );
  }

  private clearRetry(): void {
    if (this.retryTimer !== null) {
      clearTimeout(this.retryTimer);
      this.retryTimer = null;
    }
  }

  // ---------------------------------------------------------------------------------------------
  // Microphone

  private async enableMicrophone(): Promise<void> {
    const room = this.room;
    const lk = this.lk;
    if (!room || !lk) return;
    this.update({ mic: 'starting', micError: null });
    let publication: LocalTrackPublication | undefined;
    try {
      publication = await room.localParticipant.setMicrophoneEnabled(true, AUDIO_CONSTRAINTS);
    } catch (error) {
      if (room !== this.room) return;
      const kind = await explainMediaError(error);
      if (room !== this.room) return;
      this.micFailed(kind);
      return;
    }
    const track = publication?.audioTrack;
    if (publication?.trackSid) this.localTrackSids.add(publication.trackSid);
    if (room !== this.room) {
      if (track) await room.localParticipant.unpublishTrack(track, true).catch(() => track.stop());
      return;
    }
    if (!track) {
      this.micFailed('unknown');
      return;
    }
    this.micTrack = track;
    track.on(lk.TrackEvent.Ended, this.onMicEnded);
    this.update({ mic: 'on' });
  }

  private async disableMicrophone(): Promise<void> {
    const room = this.room;
    const track = this.micTrack;
    if (!room || !track) return;
    this.releaseMicTrackHandle();
    try {
      await room.localParticipant.unpublishTrack(track, true);
    } catch {
      // Disconnected meanwhile; stopping the capture below is what matters.
    } finally {
      track.stop();
    }
    this.update({ mic: this.snapshot.mic === 'error' ? 'error' : 'off' });
  }

  /** Forgets the published microphone track (and its ended listener); returns it for stopping. */
  private releaseMicTrackHandle(): LocalAudioTrack | null {
    const track = this.micTrack;
    if (track && this.lk) track.off(this.lk.TrackEvent.Ended, this.onMicEnded);
    this.micTrack = null;
    return track;
  }

  private micFailed(kind: MediaErrorKind): void {
    this.micBlocked = true;
    this.update({ mic: 'error', micError: kind });
    this.callbacks.onMicrophoneLost(kind);
  }

  /** The capture device went away (unplugged, revoked): stop publishing at once and ask for a pause. */
  private readonly onMicEnded = (): void => {
    this.micFailed('device_lost');
    this.kick();
  };

  // ---------------------------------------------------------------------------------------------
  // Room events

  private bind(lk: LiveKit, room: Room): void {
    const on = <E extends RoomEvent>(event: E, handler: (...args: never[]) => void) => {
      room.on(event, handler as never);
      this.roomBindings.push([event, handler]);
    };
    on(lk.RoomEvent.SignalReconnecting, this.onReconnecting);
    on(lk.RoomEvent.Reconnecting, this.onReconnecting);
    on(lk.RoomEvent.Reconnected, this.onReconnected);
    on(lk.RoomEvent.Disconnected, this.onDisconnected);
    on(lk.RoomEvent.TrackSubscribed, this.onTrackSubscribed);
    on(lk.RoomEvent.TrackUnsubscribed, this.onTrackUnsubscribed);
    on(lk.RoomEvent.AudioPlaybackStatusChanged, this.onPlaybackChanged);
    on(lk.RoomEvent.ActiveSpeakersChanged, this.onAgentChanged);
    on(lk.RoomEvent.ParticipantAttributesChanged, this.onAgentChanged);
    on(lk.RoomEvent.ParticipantConnected, this.onAgentChanged);
    on(lk.RoomEvent.ParticipantDisconnected, this.onAgentChanged);
    on(lk.RoomEvent.LocalTrackPublished, this.onLocalTrackPublished);
    room.registerTextStreamHandler(TRANSCRIPTION_TOPIC, this.onTranscription);
  }

  private unbind(room: Room): void {
    for (const [event, handler] of this.roomBindings) room.off(event, handler as never);
    this.roomBindings = [];
    try {
      room.unregisterTextStreamHandler(TRANSCRIPTION_TOPIC);
    } catch {
      // Not registered.
    }
  }

  private readonly onReconnecting = (): void => {
    if (this.room) this.update({ connection: 'reconnecting' });
  };

  private readonly onReconnected = (): void => {
    if (!this.room) return;
    this.update({ connection: 'connected' });
    this.kick();
  };

  private readonly onDisconnected = (reason?: DisconnectReason): void => {
    const room = this.room;
    if (!room) return;
    this.room = null;
    this.canPublishAudio = false;
    this.releaseMicTrackHandle()?.stop();
    this.unbind(room);
    this.detachAudio();
    const reset = { agentSpeaking: false, mic: this.snapshot.mic === 'error' ? 'error' : 'off', interimCaption: null } as const;
    const reasons = this.lk?.DisconnectReason;
    if (reasons && reason === reasons.DUPLICATE_IDENTITY) {
      // Another tab joined as the candidate. Do not fight over the room; the candidate decides.
      this.update({ ...reset, connection: 'failed', failure: 'opened_elsewhere' });
      return;
    }
    this.callbacks.onUnexpectedDisconnect();
    const closedByServer =
      reasons !== undefined &&
      (reason === reasons.ROOM_DELETED || reason === reasons.PARTICIPANT_REMOVED || reason === reasons.ROOM_CLOSED);
    // Give the state poll time to report completion or withdrawal before rejoining.
    this.update(reset);
    this.scheduleRetry(closedByServer ? 3_000 : 0);
  };

  private readonly onTrackSubscribed = (
    track: RemoteTrack,
    _publication: RemoteTrackPublication,
    participant: RemoteParticipant,
  ): void => {
    const room = this.room;
    const lk = this.lk;
    if (!room || !lk || track.kind !== lk.Track.Kind.Audio) return;
    if (!this.isInterviewer(room, participant)) return;
    if (this.interviewerAudio && this.interviewerAudio !== track && this.audioElement) {
      this.interviewerAudio.detach(this.audioElement);
    }
    this.interviewerAudio = track;
    if (this.audioElement) track.attach(this.audioElement);
    this.update({ canPlaybackAudio: room.canPlaybackAudio });
  };

  private readonly onTrackUnsubscribed = (track: RemoteTrack): void => {
    if (track !== this.interviewerAudio) return;
    if (this.audioElement) track.detach(this.audioElement);
    this.interviewerAudio = null;
  };

  private readonly onPlaybackChanged = (): void => {
    if (this.room) this.update({ canPlaybackAudio: this.room.canPlaybackAudio });
  };

  private readonly onAgentChanged = (): void => {
    if (this.room) this.refreshAgentState(this.room);
  };

  private readonly onLocalTrackPublished = (publication: LocalTrackPublication): void => {
    if (publication.trackSid) this.localTrackSids.add(publication.trackSid);
  };

  /** Only the agent's audio is played. Servers that do not mark agents fall back to any remote participant. */
  private isInterviewer(room: Room, participant: RemoteParticipant): boolean {
    if (participant.isAgent) return true;
    for (const p of room.remoteParticipants.values()) {
      if (p.isAgent) return false;
    }
    return true;
  }

  private refreshAgentState(room: Room): void {
    const remotes = [...room.remoteParticipants.values()];
    const agent = remotes.find((p) => p.isAgent) ?? remotes[0];
    let speaking = false;
    if (agent) {
      const state = agent.attributes[AGENT_STATE_ATTRIBUTE];
      speaking = state !== undefined ? state === 'speaking' : agent.isSpeaking;
    }
    if (speaking !== this.snapshot.agentSpeaking) this.update({ agentSpeaking: speaking });
  }

  private readonly onTranscription = (reader: TextStreamReader, participantInfo: { identity: string }): void => {
    void this.readTranscription(reader, participantInfo.identity);
  };

  private async readTranscription(reader: TextStreamReader, senderIdentity: string): Promise<void> {
    const room = this.room;
    if (!room) return;
    const attributes = reader.info.attributes ?? {};
    const show = isInterviewerCaption({
      senderIdentity,
      localIdentity: room.localParticipant.identity,
      transcribedTrackSid: attributes[ATTR_TRANSCRIBED_TRACK],
      localTrackSids: this.localTrackSids,
    });
    const id = attributes[ATTR_SEGMENT_ID] || reader.info.id;
    let text = '';
    try {
      for await (const chunk of reader) {
        // Streams that are not shown are still drained so they do not buffer.
        if (!show || room !== this.room) continue;
        text = appendCaptionChunk(text, chunk);
        this.update({ interimCaption: { id, text } });
      }
    } catch {
      // Stream aborted (disconnect); keep what arrived.
    }
    if (!show || room !== this.room) return;
    const interim = this.snapshot.interimCaption;
    this.update({
      captions: upsertCaption(this.snapshot.captions, { id, text }),
      interimCaption: interim !== null && interim.id === id ? null : interim,
    });
  }

  private detachAudio(): void {
    const element = this.audioElement;
    if (this.interviewerAudio && element) this.interviewerAudio.detach(element);
    this.interviewerAudio = null;
    if (element) {
      element.pause();
      element.srcObject = null;
    }
  }

  private update(partial: Partial<RealtimeSnapshot>): void {
    this.snapshot = { ...this.snapshot, ...partial };
    for (const listener of this.listeners) listener();
  }
}
