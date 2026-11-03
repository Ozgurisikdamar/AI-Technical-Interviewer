'use client';

import { useEffect, useState, useSyncExternalStore } from 'react';
import { issueRealtimeSession } from '@/lib/api/interviews';
import type { InterviewCandidateView } from '@/lib/api/types';
import { realtimeDesire } from '@/lib/interview/viewModel';
import {
  INITIAL_REALTIME_SNAPSHOT,
  RealtimeController,
  type RealtimeCallbacks,
  type RealtimeSnapshot,
} from '@/lib/realtime/controller';

export interface RealtimeApi {
  readonly snapshot: RealtimeSnapshot;
  /** Callback ref for the <audio> element that plays the interviewer. */
  readonly setAudioElement: (element: HTMLAudioElement | null) => void;
  readonly startAudio: () => void;
  readonly retry: () => void;
  readonly retryMicrophone: () => void;
}

function getServerSnapshot(): RealtimeSnapshot {
  return INITIAL_REALTIME_SNAPSHOT;
}

/**
 * Binds the LiveKit controller to the interview view: joins when the server allows `join_media`,
 * publishes the microphone only in voice mode while audio intake is open (never while PAUSED),
 * and leaves when the interview ends or the page unmounts. livekit-client is loaded lazily.
 */
export function useRealtime(
  interviewId: string,
  view: InterviewCandidateView | null,
  callbacks: RealtimeCallbacks,
): RealtimeApi {
  const [controller] = useState(
    () =>
      new RealtimeController({
        loadLiveKit: () => import('livekit-client'),
        requestSession: () => issueRealtimeSession(interviewId),
      }),
  );

  useEffect(() => {
    controller.setCallbacks(callbacks);
  }, [controller, callbacks]);

  useEffect(() => {
    controller.activate();
    return () => controller.deactivate();
  }, [controller]);

  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot, getServerSnapshot);
  const desire = realtimeDesire(view, snapshot.connection);

  useEffect(() => {
    controller.setDesired(desire.connect, desire.microphone);
  }, [controller, desire.connect, desire.microphone]);

  return {
    snapshot,
    setAudioElement: controller.setAudioElement,
    startAudio: () => void controller.startAudio(),
    retry: () => controller.retry(),
    retryMicrophone: () => controller.retryMicrophone(),
  };
}
