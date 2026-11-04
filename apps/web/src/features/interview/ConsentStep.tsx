'use client';

import { useEffect, useId, useState, type FormEvent } from 'react';
import { ConfirmDialog } from '@/components/ConfirmDialog';
import { ErrorNotice } from '@/components/ErrorNotice';
import { ErrorSummary } from '@/components/ErrorSummary';
import ui from '@/components/ui.module.css';
import { useFocusOnMount } from '@/components/useFocusOnMount';
import { useMessages } from '@/i18n/client';
import { label } from '@/i18n/label';
import { isAbortError, toApiError, type ApiError } from '@/lib/api/errors';
import { getConsentNotice } from '@/lib/api/interviews';
import type { ConsentNoticeView, InterviewCandidateView, NoticePurpose } from '@/lib/api/types';
import { VOICE_PURPOSE_ID, buildConsentRequest, validateConsent, type Decisions } from '@/lib/consent/validation';
import { consentSubmitEnabled } from '@/lib/interview/viewModel';
import styles from './ConsentStep.module.css';
import type { InterviewActions } from './useInterviewActions';

type NoticeState =
  | { readonly status: 'loading' }
  | { readonly status: 'ready'; readonly data: ConsentNoticeView }
  | { readonly status: 'error'; readonly error: ApiError };

interface ConsentStepProps {
  view: InterviewCandidateView;
  actions: InterviewActions;
  focusOnMount: boolean;
  /** Called after the decisions were recorded; tells the setup step whether voice was declined. */
  onSubmitted: (voiceDeclined: boolean) => void;
}

/** CREATED / CONSENT_PENDING: the pinned notice and one explicit decision per purpose. */
export function ConsentStep({ view, actions, focusOnMount, onSubmitted }: ConsentStepProps) {
  const t = useMessages();
  const headingRef = useFocusOnMount<HTMLHeadingElement>(focusOnMount);
  const [notice, setNotice] = useState<NoticeState>({ status: 'loading' });
  const [reloads, setReloads] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    getConsentNotice(view.id, { signal: controller.signal }).then(
      (data) => setNotice({ status: 'ready', data }),
      (error: unknown) => {
        if (!isAbortError(error)) setNotice({ status: 'error', error: toApiError(error) });
      },
    );
    return () => controller.abort();
  }, [view.id, reloads]);

  const ready = notice.status === 'ready' ? notice.data.notice : null;

  return (
    <section aria-labelledby="consent-heading">
      <h1 id="consent-heading" ref={headingRef} tabIndex={-1} lang={ready?.language}>
        {ready ? ready.title : t.interview.titles.consent}
      </h1>
      {notice.status === 'loading' ? <p role="status">{t.consent.loadingNotice}</p> : null}
      {notice.status === 'error' ? (
        <ErrorNotice
          error={notice.error}
          onRetry={() => {
            setNotice({ status: 'loading' });
            setReloads((n) => n + 1);
          }}
        />
      ) : null}
      {notice.status === 'ready' ? (
        <ConsentForm view={view} actions={actions} data={notice.data} onSubmitted={onSubmitted} />
      ) : null}
    </section>
  );
}

interface ConsentFormProps {
  view: InterviewCandidateView;
  actions: InterviewActions;
  data: ConsentNoticeView;
  onSubmitted: (voiceDeclined: boolean) => void;
}

function ConsentForm({ view, actions, data, onSubmitted }: ConsentFormProps) {
  const t = useMessages();
  const { notice, retention_days: retention } = data;
  const [decisions, setDecisions] = useState<Decisions>({});
  const [failedAttempts, setFailedAttempts] = useState(0);
  const [confirming, setConfirming] = useState(false);
  const formId = useId();

  const validation = validateConsent(notice.purposes, decisions);
  const showErrors = failedAttempts > 0 && !validation.complete;
  const busy = actions.pending !== null;
  const canSubmit = consentSubmitEnabled(view, busy);
  const titleOf = (purposeId: string) => notice.purposes.find((p) => p.purpose_id === purposeId)?.title ?? purposeId;

  async function send() {
    setConfirming(false);
    const result = await actions.submitConsent(view, buildConsentRequest(notice, decisions));
    if (result.ok) onSubmitted(validation.voiceDeclined);
  }

  function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!canSubmit) return;
    if (!validation.complete) {
      setFailedAttempts((n) => n + 1);
      return;
    }
    if (validation.declinedRequired.length > 0) {
      setConfirming(true);
      return;
    }
    void send();
  }

  return (
    <form onSubmit={onSubmit} noValidate className={styles.form}>
      {notice.status === 'draft' ? (
        <div className={styles.draft} role="note" data-testid="draft-banner">
          <strong className={styles.draftLabel}>DRAFT</strong> <span>{t.consent.draftBanner}</span>
        </div>
      ) : null}

      <div lang={notice.language} className={styles.sections}>
        {notice.sections.map((section, index) => (
          <section key={index} aria-labelledby={`${formId}-section-${index}`}>
            <h2 id={`${formId}-section-${index}`}>{section.heading}</h2>
            <p className={styles.sectionText}>{section.text}</p>
          </section>
        ))}
      </div>

      <h2>{t.consent.choicesHeading}</h2>
      <p>{t.consent.choicesIntro}</p>
      <div className={ui.info}>
        <p>{t.consent.requiredExplanation}</p>
        <p>{t.consent.voiceExplanation}</p>
      </div>

      <ErrorSummary
        heading={t.consent.errorSummaryHeading}
        attempt={failedAttempts}
        items={validation.missing.map((purposeId) => ({
          fieldId: `${formId}-${purposeId}-yes`,
          text: t.consent.errorSummaryItem(titleOf(purposeId)),
        }))}
      />

      {notice.purposes.map((purpose) => (
        <PurposeChoice
          key={purpose.purpose_id}
          idPrefix={`${formId}-${purpose.purpose_id}`}
          purpose={purpose}
          language={notice.language}
          retentionDays={retention[purpose.retention_category]}
          decision={decisions[purpose.purpose_id]}
          showError={showErrors && validation.missing.includes(purpose.purpose_id)}
          onChange={(granted) => setDecisions((current) => ({ ...current, [purpose.purpose_id]: granted }))}
        />
      ))}

      {actions.failure?.action === 'consent' ? <ErrorNotice error={actions.failure.error} /> : null}

      <div className={ui.actions}>
        <button
          type="submit"
          className={`${ui.button} ${ui.primary}`}
          disabled={!canSubmit}
          aria-busy={actions.pending === 'consent'}
          data-testid="consent-submit"
        >
          {actions.pending === 'consent' ? t.consent.submitting : t.consent.submit}
        </button>
      </div>

      <ConfirmDialog
        open={confirming}
        title={t.consent.confirmDeclineTitle}
        confirmLabel={t.consent.confirmDeclineConfirm}
        cancelLabel={t.consent.confirmDeclineCancel}
        onConfirm={() => void send()}
        onCancel={() => setConfirming(false)}
        confirmTestId="consent-decline-confirm"
        busy={busy}
      >
        <p>{t.consent.confirmDeclineBody(validation.declinedRequired.map(titleOf))}</p>
      </ConfirmDialog>
    </form>
  );
}

interface PurposeChoiceProps {
  idPrefix: string;
  purpose: NoticePurpose;
  language: string;
  retentionDays: number | null;
  decision: boolean | undefined;
  showError: boolean;
  onChange: (granted: boolean) => void;
}

function PurposeChoice({ idPrefix, purpose, language, retentionDays, decision, showError, onChange }: PurposeChoiceProps) {
  const t = useMessages();
  const categories = purpose.data_categories.map((c) => label(t.consent.dataCategory, c) ?? t.consent.unknownCategory).join(', ');
  const describedBy = [`${idPrefix}-description`, `${idPrefix}-details`, showError ? `${idPrefix}-error` : '']
    .filter(Boolean)
    .join(' ');

  return (
    <fieldset className={styles.purpose} data-testid={`purpose-${purpose.purpose_id}`}>
      <legend className={styles.legend}>
        <span lang={language}>{purpose.title}</span>{' '}
        <span className={`${ui.badge} ${purpose.required ? styles.required : styles.optional}`}>
          {purpose.required ? t.common.required : t.common.optional}
        </span>
      </legend>
      <p id={`${idPrefix}-description`} lang={language}>
        {purpose.description}
      </p>
      <dl id={`${idPrefix}-details`} className={styles.details}>
        <div>
          <dt>{t.consent.dataInvolved}</dt>
          <dd>{categories}</dd>
        </div>
        <div>
          <dt>{t.consent.keptFor}</dt>
          <dd>{retentionDays === null ? t.consent.notKept : t.consent.days(retentionDays)}</dd>
        </div>
        <div>
          <dt>{t.consent.legalBasis}</dt>
          <dd>{label(t.consent.basis, purpose.basis) ?? t.consent.unknownBasis}</dd>
        </div>
      </dl>
      {showError ? (
        <p id={`${idPrefix}-error`} className={styles.fieldError}>
          {t.consent.missingChoice}
        </p>
      ) : null}
      <div className={styles.options}>
        <label className={styles.option} htmlFor={`${idPrefix}-yes`}>
          <input
            type="radio"
            id={`${idPrefix}-yes`}
            name={idPrefix}
            value="yes"
            checked={decision === true}
            onChange={() => onChange(true)}
            aria-describedby={describedBy}
            data-testid={`consent-${purpose.purpose_id}-yes`}
          />
          <span>{t.consent.yes}</span>
        </label>
        <label className={styles.option} htmlFor={`${idPrefix}-no`}>
          <input
            type="radio"
            id={`${idPrefix}-no`}
            name={idPrefix}
            value="no"
            checked={decision === false}
            onChange={() => onChange(false)}
            aria-describedby={describedBy}
            data-testid={`consent-${purpose.purpose_id}-no`}
          />
          <span>{t.consent.no}</span>
        </label>
      </div>
      <div aria-live="polite">
        {decision === false && purpose.required ? <p className={ui.warning}>{t.consent.declineRequiredWarning}</p> : null}
        {decision === false && purpose.purpose_id === VOICE_PURPOSE_ID ? (
          <p className={ui.info}>{t.consent.declineVoiceNote}</p>
        ) : null}
      </div>
    </fieldset>
  );
}
