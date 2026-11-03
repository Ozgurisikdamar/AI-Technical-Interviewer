'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import ui from '@/components/ui.module.css';
import { useMessages } from '@/i18n/client';
import type { Messages } from '@/i18n';
import type { InterviewCandidateView } from '@/lib/api/types';
import { progressLabelValues, screenForState, type Screen } from '@/lib/interview/viewModel';
import { ConsentStep } from './ConsentStep';
import { CancelledStep, CompletedStep } from './EndSteps';
import { InterviewRoom } from './InterviewRoom';
import { SetupStep } from './SetupStep';
import { useInterviewActions, type InterviewActions } from './useInterviewActions';
import { useInterviewView } from './useInterviewView';
import { useRealtime, type RealtimeApi } from './useRealtime';

/** What the polite live region says for the current state (announced when it changes). */
function announcementFor(t: Messages, view: InterviewCandidateView): string {
  const a = t.interview.announcements;
  switch (screenForState(view.state)) {
    case 'consent':
      return a.consent;
    case 'setup':
      return a.setup;
    case 'completed':
      return a.completed;
    case 'cancelled':
      return a.cancelled;
    case 'waiting':
      return '';
    case 'room': {
      if (view.state === 'PAUSED') return a.paused;
      if (view.state === 'CALIBRATION') return a.calibration;
      if (view.pending_completion) return a.finishing;
      if (view.active_prompt?.kind === 'follow_up') return a.followUp;
      const { done, total } = progressLabelValues(view);
      return a.question(Math.min(done + 1, total), total);
    }
  }
}

/**
 * The candidate interview page. State comes from short-polling the server; every screen is a pure
 * function of the latest candidate view.
 */
export function InterviewApp({ interviewId }: { interviewId: string }) {
  const t = useMessages();
  const interview = useInterviewView(interviewId);
  const actions = useInterviewActions(interviewId, interview);
  const { pauseForDeviceLoss } = actions;
  const { refresh } = interview;
  const realtimeCallbacks = useMemo(
    () => ({ onMicrophoneLost: pauseForDeviceLoss, onUnexpectedDisconnect: refresh }),
    [pauseForDeviceLoss, refresh],
  );
  const realtime = useRealtime(interviewId, interview.sessionLost ? null : interview.view, realtimeCallbacks);
  const { setAudioElement } = realtime;
  const [voiceDeclined, setVoiceDeclined] = useState(false);
  const [transient, setTransient] = useState<{ id: number; text: string } | null>(null);
  const announce = useCallback((text: string) => setTransient((prev) => ({ id: (prev?.id ?? 0) + 1, text })), []);

  const { view, sessionLost, pollError } = interview;
  const screen: Screen | 'loading' | 'session_lost' = sessionLost
    ? 'session_lost'
    : view === null
      ? 'loading'
      : screenForState(view.state);

  const title =
    screen === 'session_lost' || screen === 'loading' ? t.meta.interviewTitle : t.interview.titles[screen];
  useEffect(() => {
    document.title = t.meta.titleTemplate.replace('%s', title);
  }, [t, title]);

  return (
    <div className={ui.page}>
      <div className="visually-hidden" role="status" aria-live="polite" data-testid="announcer">
        {view !== null && !sessionLost ? announcementFor(t, view) : ''}
      </div>
      <div className="visually-hidden" aria-live="polite">
        {transient !== null ? <span key={transient.id}>{transient.text}</span> : null}
      </div>

      {pollError !== null && view !== null && !sessionLost ? (
        <div className={ui.warning} role="status">
          <p>{t.interview.connectionProblem}</p>
        </div>
      ) : null}

      {sessionLost ? (
        <section aria-labelledby="lost-heading">
          <h1 id="lost-heading">{t.interview.sessionLostHeading}</h1>
          <p className={ui.lead}>{t.interview.sessionLostBody}</p>
        </section>
      ) : view === null ? (
        <section aria-labelledby="loading-heading">
          <h1 id="loading-heading">{t.meta.interviewTitle}</h1>
          <p role="status">{pollError !== null ? t.interview.connectionProblem : t.interview.loading}</p>
        </section>
      ) : (
        <Screens
          view={view}
          actions={actions}
          realtime={realtime}
          voiceDeclined={voiceDeclined}
          onConsentSubmitted={setVoiceDeclined}
          onAnnounce={announce}
        />
      )}

      {/* The interviewer's audio. Captions are rendered as live text in the Captions region; a
          WebRTC stream cannot carry a <track>. */}
      {/* eslint-disable-next-line jsx-a11y/media-has-caption */}
      <audio ref={setAudioElement} autoPlay hidden data-testid="agent-audio" />
    </div>
  );
}

interface ScreensProps {
  view: InterviewCandidateView;
  actions: InterviewActions;
  realtime: RealtimeApi;
  voiceDeclined: boolean;
  onConsentSubmitted: (voiceDeclined: boolean) => void;
  onAnnounce: (text: string) => void;
}

function Screens({ view, actions, realtime, voiceDeclined, onConsentSubmitted, onAnnounce }: ScreensProps) {
  const t = useMessages();
  const screen = screenForState(view.state);
  // Focus moves to the new step's heading only after a state change, not on first load.
  const [initialScreen] = useState(screen);
  const focusOnMount = screen !== initialScreen;

  switch (screen) {
    case 'consent':
      return <ConsentStep view={view} actions={actions} focusOnMount={focusOnMount} onSubmitted={onConsentSubmitted} />;
    case 'setup':
      return <SetupStep view={view} actions={actions} focusOnMount={focusOnMount} voiceDeclined={voiceDeclined} />;
    case 'room':
      return (
        <InterviewRoom
          view={view}
          actions={actions}
          realtime={realtime}
          focusOnMount={focusOnMount}
          onAnnounce={onAnnounce}
        />
      );
    case 'completed':
      return <CompletedStep focusOnMount={focusOnMount} realtime={realtime} />;
    case 'cancelled':
      return <CancelledStep focusOnMount={focusOnMount} />;
    case 'waiting':
      return (
        <section aria-labelledby="waiting-heading">
          <h1 id="waiting-heading">{t.interview.waitingHeading}</h1>
          <p role="status">{t.interview.waitingBody}</p>
        </section>
      );
  }
}
