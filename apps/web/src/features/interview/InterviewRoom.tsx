'use client';

import { useState, type FormEvent, type KeyboardEvent } from 'react';
import { ConfirmDialog } from '@/components/ConfirmDialog';
import { ErrorNotice } from '@/components/ErrorNotice';
import ui from '@/components/ui.module.css';
import { useMessages } from '@/i18n/client';
import { TEXT_RESPONSE_MAX_LENGTH } from '@/lib/api/interviews';
import type { InterviewCandidateView } from '@/lib/api/types';
import { progressLabelValues, roomControls, roomStatus } from '@/lib/interview/viewModel';
import { Captions } from './Captions';
import styles from './InterviewRoom.module.css';
import type { InterviewActions } from './useInterviewActions';
import { useFocusOnMount } from '@/components/useFocusOnMount';
import type { RealtimeApi } from './useRealtime';

interface InterviewRoomProps {
  view: InterviewCandidateView;
  actions: InterviewActions;
  realtime: RealtimeApi;
  focusOnMount: boolean;
  onAnnounce: (message: string) => void;
}

/** CALIBRATION, QUESTION_ACTIVE, CANDIDATE_SPEAKING and PAUSED. */
export function InterviewRoom({ view, actions, realtime, focusOnMount, onAnnounce }: InterviewRoomProps) {
  const t = useMessages();
  const headingRef = useFocusOnMount<HTMLHeadingElement>(focusOnMount);
  const [confirmWithdraw, setConfirmWithdraw] = useState(false);
  const { snapshot } = realtime;
  const busy = actions.pending !== null;
  const controls = roomControls(view, { busy });
  const status = roomStatus({ state: view.state, connection: snapshot.connection, agentSpeaking: snapshot.agentSpeaking });
  const textMode = view.input_mode === 'text';
  const paused = view.state === 'PAUSED';
  const { done, total } = progressLabelValues(view);
  const statusText = status === 'listening' && textMode ? t.room.listeningText : t.room.status[status];
  const micText = snapshot.mic === 'on' ? t.room.micOn : snapshot.mic === 'starting' ? t.room.micStarting : t.room.micOff;
  const failure = actions.failure;
  const roomFailure =
    failure !== null && failure.action !== 'consent' && failure.action !== 'start' && failure.action !== 'text_response'
      ? failure.error
      : null;

  return (
    <section aria-labelledby="room-heading" className={styles.room}>
      <div className={styles.top}>
        <h1 id="room-heading" ref={headingRef} tabIndex={-1} className={styles.heading}>
          {t.room.heading}
        </h1>
        <p className={styles.progress} data-testid="progress">
          {t.room.progress(done, total)}
        </p>
      </div>

      <div className={styles.statusBar}>
        <p className={styles.status} data-testid="status" data-status={status}>
          <span className="visually-hidden">{t.room.statusLabel} </span>
          <span className={`${styles.dot} ${styles[`dot_${status}`] ?? ''}`} aria-hidden="true" />
          {statusText}
        </p>
        {view.input_mode === 'voice' ? (
          <p className={styles.chip} data-testid="mic-indicator" data-mic={snapshot.mic}>
            {micText}
          </p>
        ) : null}
        {view.recording_active ? (
          <p className={`${styles.chip} ${styles.recording}`} data-testid="recording-indicator">
            <span aria-hidden="true">● </span>
            {t.room.recording}
          </p>
        ) : null}
      </div>

      {!snapshot.canPlaybackAudio && snapshot.connection === 'connected' ? (
        <div className={ui.warning}>
          <p>{t.room.enableAudioHint}</p>
          <button
            type="button"
            className={`${ui.button} ${ui.primary}`}
            onClick={realtime.startAudio}
            data-testid="enable-audio"
          >
            {t.room.enableAudio}
          </button>
        </div>
      ) : null}

      <RealtimeProblems realtime={realtime} paused={paused} voice={view.input_mode === 'voice'} />

      {view.state === 'CALIBRATION' ? (
        <div className={ui.info}>
          <h2 className={styles.noticeHeading}>{t.room.calibrationHeading}</h2>
          <p>{textMode ? t.room.calibrationBodyText : t.room.calibrationBody}</p>
        </div>
      ) : null}

      {paused ? (
        <div className={ui.warning} data-testid="paused-notice">
          <h2 className={styles.noticeHeading}>{t.room.pausedHeading}</h2>
          <p>{textMode ? t.room.pausedBodyText : t.room.pausedBody}</p>
        </div>
      ) : null}

      <section aria-labelledby="prompt-heading" className={styles.prompt}>
        <h2 id="prompt-heading" className={styles.promptHeading}>
          {view.active_prompt?.kind === 'follow_up' ? t.room.followUpHeading : t.room.promptHeading}
        </h2>
        <p className={styles.promptText} data-testid="prompt">
          {view.active_prompt?.text ?? t.room.promptWaiting}
        </p>
      </section>

      <Captions captions={snapshot.captions} interim={snapshot.interimCaption} />

      {controls.textInput.visible && view.active_prompt !== null ? (
        <TextAnswer key={view.active_prompt.activation_id} view={view} actions={actions} onSent={onAnnounce} />
      ) : null}

      <div role="group" aria-label={t.room.controlsLabel} className={styles.controls}>
        {controls.begin.visible ? (
          <button
            type="button"
            className={`${ui.button} ${ui.primary}`}
            onClick={() => void actions.begin(view)}
            disabled={!controls.begin.enabled}
            data-testid="begin"
          >
            {t.room.begin}
          </button>
        ) : null}
        {controls.finishAnswer.visible ? (
          <button
            type="button"
            className={`${ui.button} ${ui.primary}`}
            onClick={() => void actions.finishAnswer(view)}
            disabled={!controls.finishAnswer.enabled}
            aria-busy={controls.finishing}
            data-testid="finish-answer"
          >
            {controls.finishing || actions.pending === 'finish_answer' ? t.room.finishing : t.room.finishAnswer}
          </button>
        ) : null}
        {controls.resume.visible ? (
          <button
            type="button"
            className={`${ui.button} ${ui.primary}`}
            onClick={() => void actions.resume(view)}
            disabled={!controls.resume.enabled}
            data-testid="resume"
          >
            {t.room.resume}
          </button>
        ) : null}
        {controls.pause.visible ? (
          <button
            type="button"
            className={`${ui.button} ${ui.secondary}`}
            onClick={() => void actions.pause(view)}
            disabled={!controls.pause.enabled}
            data-testid="pause"
          >
            {t.room.pause}
          </button>
        ) : null}
        {controls.withdraw.visible ? (
          <button
            type="button"
            className={`${ui.button} ${ui.danger}`}
            onClick={() => setConfirmWithdraw(true)}
            disabled={!controls.withdraw.enabled}
            data-testid="withdraw"
          >
            {t.room.withdraw}
          </button>
        ) : null}
      </div>
      {controls.finishAnswer.visible ? (
        <p className={ui.muted}>{textMode ? t.room.finishHintText : t.room.finishHint}</p>
      ) : null}

      {roomFailure !== null ? <ErrorNotice error={roomFailure} /> : null}

      <ConfirmDialog
        open={confirmWithdraw}
        title={t.room.confirmWithdrawTitle}
        confirmLabel={t.room.confirmWithdrawConfirm}
        cancelLabel={t.room.confirmWithdrawCancel}
        onCancel={() => setConfirmWithdraw(false)}
        onConfirm={() => {
          setConfirmWithdraw(false);
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

function RealtimeProblems({ realtime, paused, voice }: { realtime: RealtimeApi; paused: boolean; voice: boolean }) {
  const t = useMessages();
  const { snapshot } = realtime;
  return (
    <>
      {snapshot.failure === 'opened_elsewhere' ? (
        <div className={ui.warning} role="alert">
          <p>{t.room.openedElsewhere}</p>
          <button type="button" className={`${ui.button} ${ui.secondary}`} onClick={realtime.retry}>
            {t.room.useHere}
          </button>
        </div>
      ) : null}
      {snapshot.failure === 'connect_failed' ? (
        <div className={ui.error} role="alert">
          <p>{t.room.connectionFailed}</p>
          <button type="button" className={`${ui.button} ${ui.secondary}`} onClick={realtime.retry}>
            {t.room.reconnect}
          </button>
        </div>
      ) : null}
      {snapshot.failure === 'unsupported_browser' ? (
        <div className={ui.error} role="alert">
          <p>{t.room.unsupportedBrowser}</p>
        </div>
      ) : null}
      {voice && snapshot.micError !== null ? (
        <div className={ui.error} role="alert" data-testid="mic-error">
          <p>{t.room.micErrors[snapshot.micError]}</p>
          {paused ? (
            <p>{t.room.micPausedHint}</p>
          ) : (
            <button type="button" className={`${ui.button} ${ui.secondary}`} onClick={realtime.retryMicrophone}>
              {t.room.retryMic}
            </button>
          )}
        </div>
      ) : null}
    </>
  );
}

interface TextAnswerProps {
  view: InterviewCandidateView;
  actions: InterviewActions;
  onSent: (message: string) => void;
}

/** Text mode (ADR-018): answers go only to POST /api/v1/interviews/{id}/text-responses. */
function TextAnswer({ view, actions, onSent }: TextAnswerProps) {
  const t = useMessages();
  const [draft, setDraft] = useState('');
  const [sent, setSent] = useState(false);
  const max = TEXT_RESPONSE_MAX_LENGTH;
  const sending = actions.pending === 'text_response';
  const controls = roomControls(view, { busy: actions.pending !== null, draft, maxTextLength: max });
  const failure = actions.failure?.action === 'text_response' ? actions.failure.error : null;

  async function send() {
    if (!controls.textSend.enabled) return;
    const result = await actions.submitText(view, draft);
    if (result.ok) {
      setDraft('');
      setSent(true);
      onSent(t.interview.announcements.answerSent);
    }
  }

  function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    void send();
  }

  function onKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key === 'Enter' && (event.ctrlKey || event.metaKey)) {
      event.preventDefault();
      void send();
    }
  }

  return (
    <form className={styles.textForm} onSubmit={onSubmit}>
      <label htmlFor="text-answer" className={styles.textLabel}>
        {t.room.textLabel}
      </label>
      <p id="text-answer-hint" className={ui.muted}>
        {t.room.textHint(max)}
      </p>
      <textarea
        id="text-answer"
        className={styles.textarea}
        value={draft}
        onChange={(event) => {
          setDraft(event.target.value);
          setSent(false);
        }}
        onKeyDown={onKeyDown}
        maxLength={max}
        rows={6}
        readOnly={sending}
        disabled={!controls.textInput.enabled}
        aria-describedby="text-answer-hint text-answer-count"
        spellCheck
        data-testid="text-input"
      />
      <p id="text-answer-count" className={styles.counter}>
        {t.room.textCounter(draft.length, max)}
      </p>
      <div className={ui.actions}>
        <button
          type="submit"
          className={`${ui.button} ${ui.primary}`}
          disabled={!controls.textSend.enabled}
          aria-busy={sending}
          data-testid="text-send"
        >
          {sending ? t.room.textSending : t.room.textSend}
        </button>
      </div>
      <div aria-live="polite">{sent ? <p className={styles.sent}>{t.room.textSent}</p> : null}</div>
      {failure !== null ? <ErrorNotice error={failure} /> : null}
    </form>
  );
}
