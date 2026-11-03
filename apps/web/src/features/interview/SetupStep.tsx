'use client';

import { useState } from 'react';
import { ConfirmDialog } from '@/components/ConfirmDialog';
import { ErrorNotice } from '@/components/ErrorNotice';
import ui from '@/components/ui.module.css';
import { useMessages } from '@/i18n/client';
import type { InterviewCandidateView } from '@/lib/api/types';
import { setupControls } from '@/lib/interview/viewModel';
import styles from './SetupStep.module.css';
import type { InterviewActions } from './useInterviewActions';
import { useFocusOnMount } from './useFocusOnMount';
import { useMicrophoneCheck } from './useMicrophoneCheck';

interface SetupStepProps {
  view: InterviewCandidateView;
  actions: InterviewActions;
  focusOnMount: boolean;
  /** The candidate declined the live voice purpose in this page session. */
  voiceDeclined: boolean;
}

/** READY: microphone check with a live level meter, then voice or text mode. */
export function SetupStep({ view, actions, focusOnMount, voiceDeclined }: SetupStepProps) {
  const t = useMessages();
  const headingRef = useFocusOnMount<HTMLHeadingElement>(focusOnMount);
  const { status: micStatus, error: micError, voiceDetected, meterRef, start: startMicCheck, stop: stopMicCheck } =
    useMicrophoneCheck();
  const [voiceRefused, setVoiceRefused] = useState(false);
  const [confirmWithdraw, setConfirmWithdraw] = useState(false);

  const voiceUnavailable = voiceDeclined || voiceRefused || micError === 'insecure_context';
  const micFailed = micStatus === 'error';
  const busy = actions.pending !== null;
  const controls = setupControls(view, { busy, micReady: micStatus === 'live', voiceUnavailable });
  const startFailure = actions.failure?.action === 'start' ? actions.failure.error : null;

  async function start(mode: 'voice' | 'text') {
    const result = await actions.start(view, mode);
    if (!result.ok && result.error.code === 'voice_processing_not_consented') {
      stopMicCheck();
      setVoiceRefused(true);
    }
  }

  return (
    <section aria-labelledby="setup-heading">
      <h1 id="setup-heading" ref={headingRef} tabIndex={-1}>
        {t.setup.heading}
      </h1>
      <p className={ui.lead}>{t.setup.intro}</p>
      <p>{t.setup.speakersHint}</p>

      {voiceUnavailable ? (
        <div className={ui.info} role="status">
          <p>{voiceDeclined ? t.setup.voiceDeclined : voiceRefused ? t.setup.voiceRefused : t.setup.micErrors.insecure_context}</p>
        </div>
      ) : (
        <section className={`${ui.card} ${styles.option}`} aria-labelledby="voice-heading">
          <h2 id="voice-heading">{t.setup.voiceHeading}</h2>
          <p>{t.setup.voiceIntro}</p>
          <div className={ui.actions}>
            <button
              type="button"
              className={`${ui.button} ${ui.secondary}`}
              onClick={startMicCheck}
              disabled={!controls.checkMic.enabled || micStatus === 'requesting'}
              data-testid="check-mic"
            >
              {micStatus === 'live' || micFailed ? t.setup.checkMicAgain : t.setup.checkMic}
            </button>
          </div>
          <div className={styles.meterRow}>
            <label htmlFor="mic-level" className={styles.meterLabel}>
              {t.setup.levelLabel}
            </label>
            <meter
              id="mic-level"
              ref={meterRef}
              className={styles.meter}
              min={0}
              max={1}
              low={0.25}
              high={0.9}
              optimum={0.6}
              data-testid="mic-level"
            />
          </div>
          <div aria-live="polite" className={styles.micStatus} data-testid="mic-status">
            {micStatus === 'requesting' ? <p>{t.setup.requesting}</p> : null}
            {micStatus === 'live' ? (
              <p className={styles.ok}>
                {t.setup.micReady} {voiceDetected ? t.setup.hearingYou : t.setup.speakNow}
              </p>
            ) : null}
            {micFailed && micError !== null ? (
              <div className={ui.error} role="alert">
                <p>{t.setup.micErrors[micError]}</p>
                <p>{t.setup.textRecommended}</p>
              </div>
            ) : null}
          </div>
          <div className={ui.actions}>
            <button
              type="button"
              className={`${ui.button} ${ui.primary}`}
              onClick={() => void start('voice')}
              disabled={!controls.startVoice.enabled}
              aria-busy={actions.pending === 'start'}
              data-testid="start-voice"
            >
              {actions.pending === 'start' ? t.setup.starting : t.setup.startVoice}
            </button>
          </div>
        </section>
      )}

      <section className={`${ui.card} ${styles.option}`} aria-labelledby="text-heading">
        <h2 id="text-heading">{t.setup.textHeading}</h2>
        <p>{t.setup.textIntro}</p>
        <div className={ui.actions}>
          <button
            type="button"
            className={`${ui.button} ${voiceUnavailable || micFailed ? ui.primary : ui.secondary}`}
            onClick={() => {
              stopMicCheck();
              void start('text');
            }}
            disabled={!controls.startText.enabled}
            data-testid="start-text"
          >
            {t.setup.startText}
          </button>
        </div>
      </section>

      {startFailure !== null && startFailure.code !== 'voice_processing_not_consented' ? (
        <ErrorNotice error={startFailure} />
      ) : null}
      {actions.failure?.action === 'withdraw' ? <ErrorNotice error={actions.failure.error} /> : null}

      {controls.withdraw.visible ? (
        <div className={styles.withdraw}>
          <button
            type="button"
            className={`${ui.button} ${ui.danger}`}
            onClick={() => setConfirmWithdraw(true)}
            disabled={!controls.withdraw.enabled}
            data-testid="withdraw"
          >
            {t.setup.withdraw}
          </button>
        </div>
      ) : null}

      <ConfirmDialog
        open={confirmWithdraw}
        title={t.room.confirmWithdrawTitle}
        confirmLabel={t.room.confirmWithdrawConfirm}
        cancelLabel={t.room.confirmWithdrawCancel}
        onCancel={() => setConfirmWithdraw(false)}
        onConfirm={() => {
          setConfirmWithdraw(false);
          stopMicCheck();
          void actions.withdraw(view);
        }}
        confirmTestId="withdraw-confirm"
        busy={busy}
      >
        <p>{t.room.confirmWithdrawBody}</p>
      </ConfirmDialog>
    </section>
  );
}
