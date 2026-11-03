import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type * as LiveKitModule from 'livekit-client';
import type { RealtimeSession } from '@/lib/api/types';
import { RealtimeController, type RealtimeCallbacks } from './controller';

/* ------------------------------------------------------------------------------------------ */
/* A small fake of the parts of livekit-client the controller uses.                            */

type Listener = (...args: unknown[]) => void;

class Emitter {
  private readonly listeners = new Map<string, Set<Listener>>();
  on(event: string, listener: Listener) {
    if (!this.listeners.has(event)) this.listeners.set(event, new Set());
    this.listeners.get(event)?.add(listener);
    return this;
  }
  off(event: string, listener: Listener) {
    this.listeners.get(event)?.delete(listener);
    return this;
  }
  emit(event: string, ...args: unknown[]) {
    for (const listener of [...(this.listeners.get(event) ?? [])]) listener(...args);
  }
  count(event: string) {
    return this.listeners.get(event)?.size ?? 0;
  }
}

let sidCounter = 0;

class FakeLocalAudioTrack extends Emitter {
  stopped = false;
  stop() {
    this.stopped = true;
  }
}

/** Every method that would put candidate data on the LiveKit data channel. */
const dataChannelCalls: string[] = [];

class FakeLocalParticipant {
  identity = 'candidate';
  published: FakeLocalAudioTrack | null = null;
  micCalls: boolean[] = [];
  nextFailure: Error | null = null;
  gate: Promise<void> | null = null;

  async setMicrophoneEnabled(enabled: boolean) {
    this.micCalls.push(enabled);
    if (this.gate) await this.gate;
    if (this.nextFailure) {
      const failure = this.nextFailure;
      this.nextFailure = null;
      throw failure;
    }
    if (!enabled) return undefined;
    const track = new FakeLocalAudioTrack();
    this.published = track;
    return { trackSid: `TR_local_${++sidCounter}`, audioTrack: track };
  }

  async unpublishTrack(track: FakeLocalAudioTrack, stopOnUnpublish?: boolean) {
    if (this.published === track) this.published = null;
    if (stopOnUnpublish) track.stop();
    return undefined;
  }

  publishData() {
    dataChannelCalls.push('publishData');
  }
  sendText() {
    dataChannelCalls.push('sendText');
  }
  streamText() {
    dataChannelCalls.push('streamText');
  }
  sendChatMessage() {
    dataChannelCalls.push('sendChatMessage');
  }
  performRpc() {
    dataChannelCalls.push('performRpc');
  }
}

class FakeRemoteAudioTrack {
  kind = 'audio';
  attached: unknown[] = [];
  attach(element: unknown) {
    this.attached.push(element);
    return element;
  }
  detach(element: unknown) {
    this.attached = this.attached.filter((e) => e !== element);
    return element;
  }
}

class FakeRoom extends Emitter {
  static instances: FakeRoom[] = [];
  localParticipant = new FakeLocalParticipant();
  remoteParticipants = new Map<string, { identity: string; isAgent: boolean; isSpeaking: boolean; attributes: Record<string, string> }>();
  canPlaybackAudio = true;
  connectArgs: unknown[] | null = null;
  disconnectCalls = 0;
  textHandlers = new Map<string, (reader: unknown, info: { identity: string }) => void>();
  failConnect = false;

  constructor(readonly options: unknown) {
    super();
    FakeRoom.instances.push(this);
  }
  async connect(...args: unknown[]) {
    this.connectArgs = args;
    if (this.failConnect) throw new Error('connect failed');
  }
  async disconnect() {
    this.disconnectCalls += 1;
  }
  registerTextStreamHandler(topic: string, handler: (reader: unknown, info: { identity: string }) => void) {
    if (this.textHandlers.has(topic)) throw new Error('handler already set');
    this.textHandlers.set(topic, handler);
  }
  unregisterTextStreamHandler(topic: string) {
    this.textHandlers.delete(topic);
  }
  async startAudio() {
    this.canPlaybackAudio = true;
  }
}

const RoomEvent = {
  SignalReconnecting: 'signalReconnecting',
  Reconnecting: 'reconnecting',
  Reconnected: 'reconnected',
  Disconnected: 'disconnected',
  TrackSubscribed: 'trackSubscribed',
  TrackUnsubscribed: 'trackUnsubscribed',
  AudioPlaybackStatusChanged: 'audioPlaybackChanged',
  ActiveSpeakersChanged: 'activeSpeakersChanged',
  ParticipantAttributesChanged: 'participantAttributesChanged',
  ParticipantConnected: 'participantConnected',
  ParticipantDisconnected: 'participantDisconnected',
  LocalTrackPublished: 'localTrackPublished',
};

const fakeLiveKit = {
  Room: FakeRoom,
  RoomEvent,
  TrackEvent: { Ended: 'ended' },
  Track: { Kind: { Audio: 'audio' } },
  DisconnectReason: { CLIENT_INITIATED: 1, DUPLICATE_IDENTITY: 2, PARTICIPANT_REMOVED: 4, ROOM_DELETED: 5, ROOM_CLOSED: 10 },
  LogLevel: { warn: 3 },
  setLogLevel: () => undefined,
  isBrowserSupported: () => true,
};

/* ------------------------------------------------------------------------------------------ */

function session(canPublishAudio = true): RealtimeSession {
  return {
    livekit_url: 'ws://localhost:7880',
    token: 'test-only-livekit-token',
    room: 'rm_test',
    identity: 'candidate',
    can_publish_audio: canPublishAudio,
    expires_at: '2026-11-03T12:00:00Z',
  };
}

async function flush() {
  for (let i = 0; i < 50; i++) await Promise.resolve();
}

function setup(canPublishAudio = true) {
  const requestSession = vi.fn(async () => session(canPublishAudio));
  const callbacks = { onMicrophoneLost: vi.fn(), onUnexpectedDisconnect: vi.fn() } satisfies RealtimeCallbacks;
  const controller = new RealtimeController({
    loadLiveKit: async () => fakeLiveKit as unknown as typeof LiveKitModule,
    requestSession,
  });
  controller.setCallbacks(callbacks);
  controller.activate();
  const room = () => FakeRoom.instances.at(-1) as FakeRoom;
  return { controller, requestSession, callbacks, room };
}

function textReader(id: string, chunks: string[], attributes: Record<string, string> = {}) {
  return {
    info: { id, attributes },
    async *[Symbol.asyncIterator]() {
      for (const chunk of chunks) yield chunk;
    },
  };
}

beforeEach(() => {
  FakeRoom.instances = [];
  dataChannelCalls.length = 0;
});

afterEach(() => {
  // No scenario may ever publish data over LiveKit.
  expect(dataChannelCalls).toEqual([]);
  vi.useRealTimers();
});

describe('joining', () => {
  it('requests a realtime session, connects with its token and listens for captions', async () => {
    const { controller, requestSession, room } = setup();
    controller.setDesired(true, false);
    await flush();
    expect(requestSession).toHaveBeenCalledTimes(1);
    expect(room().connectArgs?.slice(0, 2)).toEqual(['ws://localhost:7880', 'test-only-livekit-token']);
    expect(room().textHandlers.has('lk.transcription')).toBe(true);
    expect(controller.getSnapshot().connection).toBe('connected');
    expect(room().localParticipant.micCalls).toEqual([]);
  });

  it('does nothing until activated and desired', async () => {
    const { controller, requestSession } = setup();
    await flush();
    expect(requestSession).not.toHaveBeenCalled();
    controller.deactivate();
    controller.setDesired(true, true);
    await flush();
    expect(requestSession).not.toHaveBeenCalled();
  });
});

describe('microphone follows the interview state', () => {
  it('publishes in voice mode during intake, and unpublishes and stops the capture when paused', async () => {
    const { controller, room } = setup();
    controller.setDesired(true, true);
    await flush();
    const first = room().localParticipant.published;
    expect(first).not.toBeNull();
    expect(controller.getSnapshot().mic).toBe('on');

    controller.setDesired(true, false); // PAUSED
    await flush();
    expect(room().localParticipant.published).toBeNull();
    expect(first?.stopped).toBe(true);
    expect(controller.getSnapshot().mic).toBe('off');
    expect(controller.getSnapshot().connection).toBe('connected'); // still in the room

    controller.setDesired(true, true); // resumed
    await flush();
    expect(room().localParticipant.published).not.toBeNull();
    expect(room().localParticipant.published).not.toBe(first);
  });

  it('ends unpublished when a pause arrives while the microphone is still starting', async () => {
    const { controller, room } = setup();
    controller.setDesired(true, false);
    await flush();
    let release: () => void = () => undefined;
    room().localParticipant.gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    controller.setDesired(true, true);
    await flush();
    controller.setDesired(true, false); // pause while getUserMedia is pending
    room().localParticipant.gate = null;
    release();
    await flush();
    expect(room().localParticipant.published).toBeNull();
    expect(controller.getSnapshot().mic).toBe('off');
  });

  it('never publishes when the token cannot publish (text mode)', async () => {
    const { controller, room } = setup(false);
    controller.setDesired(true, true);
    await flush();
    expect(room().localParticipant.micCalls).toEqual([]);
  });

  it('on device loss: stops publishing at once, asks for a pause and does not silently re-acquire', async () => {
    const { controller, callbacks, room } = setup();
    controller.setDesired(true, true);
    await flush();
    const track = room().localParticipant.published as FakeLocalAudioTrack;
    expect(track.count('ended')).toBe(1);

    track.emit('ended');
    await flush();
    expect(callbacks.onMicrophoneLost).toHaveBeenCalledWith('device_lost');
    expect(room().localParticipant.published).toBeNull();
    expect(controller.getSnapshot().micError).toBe('device_lost');
    expect(room().localParticipant.micCalls).toEqual([true]);

    controller.setDesired(true, false); // the server paused
    controller.setDesired(true, true); // the candidate resumed
    await flush();
    expect(room().localParticipant.micCalls).toEqual([true, true]);
    expect(controller.getSnapshot().micError).toBeNull();
  });

  it('reports a microphone that cannot start (permission denied) and asks for a pause', async () => {
    const { controller, callbacks, room } = setup();
    controller.setDesired(true, false);
    await flush();
    room().localParticipant.nextFailure = new DOMException('denied', 'NotAllowedError');
    controller.setDesired(true, true);
    await flush();
    expect(callbacks.onMicrophoneLost).toHaveBeenCalledWith('permission_denied');
    expect(controller.getSnapshot().mic).toBe('error');
    expect(room().localParticipant.micCalls).toEqual([true]);
  });
});

describe('leaving and reconnecting', () => {
  it('leaves the room and stops the microphone when no longer desired', async () => {
    const { controller, room } = setup();
    controller.setDesired(true, true);
    await flush();
    const track = room().localParticipant.published;
    controller.setDesired(false, false);
    await flush();
    expect(room().disconnectCalls).toBe(1);
    expect(track?.stopped).toBe(true);
    expect(room().textHandlers.size).toBe(0);
    expect(controller.getSnapshot().connection).toBe('idle');
  });

  it('rejoins with a new session after an unexpected disconnect', async () => {
    vi.useFakeTimers();
    const { controller, requestSession, callbacks, room } = setup();
    controller.setDesired(true, true);
    await flush();
    room().emit(RoomEvent.Disconnected, 0);
    await flush();
    expect(callbacks.onUnexpectedDisconnect).toHaveBeenCalled();
    expect(controller.getSnapshot().connection).toBe('disconnected');
    await vi.advanceTimersByTimeAsync(1_000);
    await flush();
    expect(requestSession).toHaveBeenCalledTimes(2);
    expect(FakeRoom.instances).toHaveLength(2);
    expect(controller.getSnapshot().connection).toBe('connected');
  });

  it('does not fight another tab that joined as the candidate', async () => {
    vi.useFakeTimers();
    const { controller, requestSession, room } = setup();
    controller.setDesired(true, false);
    await flush();
    room().emit(RoomEvent.Disconnected, fakeLiveKit.DisconnectReason.DUPLICATE_IDENTITY);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(controller.getSnapshot().failure).toBe('opened_elsewhere');
    expect(requestSession).toHaveBeenCalledTimes(1);
    controller.retry();
    await flush();
    expect(requestSession).toHaveBeenCalledTimes(2);
  });

  it('stops retrying once the interview no longer wants media', async () => {
    vi.useFakeTimers();
    const { controller, requestSession, room } = setup();
    controller.setDesired(true, false);
    await flush();
    room().emit(RoomEvent.Disconnected, fakeLiveKit.DisconnectReason.ROOM_DELETED);
    controller.setDesired(false, false); // the poll reported CANCELLED
    await vi.advanceTimersByTimeAsync(60_000);
    expect(requestSession).toHaveBeenCalledTimes(1);
  });
});

describe('captions', () => {
  it('shows the interviewer’s speech and never the candidate’s own transcription', async () => {
    const { controller, room } = setup();
    controller.setDesired(true, true);
    await flush();
    const handler = room().textHandlers.get('lk.transcription');
    expect(handler).toBeDefined();

    handler?.(textReader('s1', ['Hello, ', 'welcome.'], { 'lk.segment_id': 'seg_a', 'lk.transcribed_track_id': 'TR_agent' }), {
      identity: 'agent-1',
    });
    handler?.(textReader('s2', ['my private answer'], { 'lk.segment_id': 'seg_b' }), { identity: 'candidate' });
    handler?.(
      textReader('s3', ['also mine'], { 'lk.segment_id': 'seg_c', 'lk.transcribed_track_id': `TR_local_${sidCounter}` }),
      { identity: 'agent-1' },
    );
    await flush();

    expect(controller.getSnapshot().captions).toEqual([{ id: 'seg_a', text: 'Hello, welcome.' }]);
    expect(controller.getSnapshot().interimCaption).toBeNull();
  });

  it('plays only the agent’s audio through the provided element', async () => {
    const { controller, room } = setup();
    const element = { pause: vi.fn(), srcObject: null } as unknown as HTMLAudioElement;
    controller.setAudioElement(element);
    controller.setDesired(true, false);
    await flush();
    room().remoteParticipants.set('agent-1', { identity: 'agent-1', isAgent: true, isSpeaking: false, attributes: {} });
    const agentTrack = new FakeRemoteAudioTrack();
    const strangerTrack = new FakeRemoteAudioTrack();
    room().emit(RoomEvent.TrackSubscribed, agentTrack, {}, { identity: 'agent-1', isAgent: true });
    room().emit(RoomEvent.TrackSubscribed, strangerTrack, {}, { identity: 'someone', isAgent: false });
    expect(agentTrack.attached).toEqual([element]);
    expect(strangerTrack.attached).toEqual([]);
  });

  it('tracks whether the interviewer is speaking from the agent state attribute', async () => {
    const { controller, room } = setup();
    controller.setDesired(true, false);
    await flush();
    const agent = { identity: 'agent-1', isAgent: true, isSpeaking: false, attributes: { 'lk.agent.state': 'speaking' } };
    room().remoteParticipants.set('agent-1', agent);
    room().emit(RoomEvent.ParticipantAttributesChanged);
    expect(controller.getSnapshot().agentSpeaking).toBe(true);
    agent.attributes['lk.agent.state'] = 'listening';
    room().emit(RoomEvent.ParticipantAttributesChanged);
    expect(controller.getSnapshot().agentSpeaking).toBe(false);
  });
});
