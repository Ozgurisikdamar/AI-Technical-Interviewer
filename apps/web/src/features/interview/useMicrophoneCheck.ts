'use client';

import { useCallback, useEffect, useRef, useState, type RefObject } from 'react';
import { isVoiceDetected, levelFromSamples } from '@/lib/audio/level';
import {
  AUDIO_CONSTRAINTS,
  classifyMediaError,
  microphoneApiAvailable,
  type MediaErrorKind,
} from '@/lib/audio/mediaErrors';

export type MicCheckStatus = 'idle' | 'requesting' | 'live' | 'error';

interface Resources {
  stream: MediaStream | null;
  context: AudioContext | null;
  frame: number;
  level: number;
  detected: boolean;
}

export interface MicrophoneCheck {
  readonly status: MicCheckStatus;
  readonly error: MediaErrorKind | null;
  readonly voiceDetected: boolean;
  /** The <meter> is updated directly at animation-frame rate, outside React renders. */
  readonly meterRef: RefObject<HTMLMeterElement | null>;
  readonly start: () => void;
  readonly stop: () => void;
}

/**
 * Device check: opens the microphone (echo cancellation, noise suppression and AGC on), measures
 * the input level with an AnalyserNode (never connected to the speakers, so nothing is played
 * back or recorded) and releases the device on stop or unmount.
 */
export function useMicrophoneCheck(): MicrophoneCheck {
  const [status, setStatus] = useState<MicCheckStatus>('idle');
  const [error, setError] = useState<MediaErrorKind | null>(null);
  const [voiceDetected, setVoiceDetected] = useState(false);
  const meterRef = useRef<HTMLMeterElement | null>(null);
  const resources = useRef<Resources | null>(null);
  const attempt = useRef(0);

  const release = useCallback(() => {
    const r = resources.current;
    resources.current = null;
    if (!r) return;
    if (r.frame) cancelAnimationFrame(r.frame);
    r.stream?.getTracks().forEach((track) => track.stop());
    if (r.context && r.context.state !== 'closed') void r.context.close().catch(() => undefined);
    if (meterRef.current) meterRef.current.value = 0;
  }, []);

  const stop = useCallback(() => {
    attempt.current += 1;
    release();
    setStatus('idle');
  }, [release]);

  const start = useCallback(() => {
    release();
    const current = ++attempt.current;
    setVoiceDetected(false);
    setError(null);
    if (!microphoneApiAvailable()) {
      setStatus('error');
      setError('insecure_context');
      return;
    }
    setStatus('requesting');
    // Create the AudioContext inside the click so autoplay policies let it run.
    const r: Resources = { stream: null, context: null, frame: 0, level: 0, detected: false };
    resources.current = r;
    try {
      r.context = new AudioContext();
      void r.context.resume().catch(() => undefined);
    } catch {
      r.context = null;
    }

    navigator.mediaDevices.getUserMedia({ audio: AUDIO_CONSTRAINTS, video: false }).then(
      (stream) => {
        if (current !== attempt.current || resources.current !== r) {
          stream.getTracks().forEach((track) => track.stop());
          return;
        }
        r.stream = stream;
        const track = stream.getAudioTracks()[0];
        track?.addEventListener('ended', () => {
          if (resources.current !== r) return;
          release();
          setStatus('error');
          setError('device_lost');
        });
        const context = r.context;
        if (context) {
          const analyser = context.createAnalyser();
          analyser.fftSize = 2048;
          analyser.smoothingTimeConstant = 0;
          context.createMediaStreamSource(stream).connect(analyser);
          const samples = new Float32Array(analyser.fftSize);
          const frame = () => {
            if (resources.current !== r) return;
            analyser.getFloatTimeDomainData(samples);
            r.level = levelFromSamples(samples, r.level);
            if (meterRef.current) meterRef.current.value = r.level;
            if (!r.detected && isVoiceDetected(r.level)) {
              r.detected = true;
              setVoiceDetected(true);
            }
            r.frame = requestAnimationFrame(frame);
          };
          r.frame = requestAnimationFrame(frame);
        }
        setStatus('live');
      },
      (caught: unknown) => {
        if (current !== attempt.current) return;
        release();
        setStatus('error');
        setError(classifyMediaError(caught));
      },
    );
  }, [release]);

  useEffect(() => release, [release]);

  return { status, error, voiceDetected, meterRef, start, stop };
}
