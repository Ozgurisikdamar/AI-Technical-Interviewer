'use client';

import { useId, useState } from 'react';
import ui from '@/components/ui.module.css';
import { useMessages } from '@/i18n/client';
import { DIMENSIONS, type AIDraft, type DimensionScores, type EvidenceItem, type ReportAIPart } from '@/lib/api/staffTypes';
import type { QuoteStatus } from '@/lib/staff/highlight';
import { formatPoints } from '@/lib/staff/weighted';
import { Reasons } from './Reasons';
import styles from './staff.module.css';

interface AIDraftProps {
  draft: AIDraft;
  /** AI-derived and unknown review reasons: shown only here. */
  reasons: readonly string[];
  /** Whether each evidence quote was found in the transcript shown (same order as the evidence). */
  quoteStatus: readonly QuoteStatus[];
  language: string;
  onOpened: () => void;
}

/**
 * The AI draft, closed until the reviewer opens it (independent first, ADR-023). Its content is
 * not rendered at all while closed. The button keeps its label; aria-expanded gives the state,
 * and focus stays on the button.
 */
export function AIDraftDisclosure({ draft, reasons, quoteStatus, language, onOpened }: AIDraftProps) {
  const t = useMessages();
  const panelId = useId();
  const [open, setOpen] = useState(false);
  return (
    <section className={styles.section} data-testid="ai-draft">
      <h2 className={styles.disclosure}>
        <button
          type="button"
          className={styles.disclosureButton}
          aria-expanded={open}
          aria-controls={open ? panelId : undefined}
          onClick={() => {
            if (!open) onOpened();
            setOpen(!open);
          }}
          data-testid="ai-draft-toggle"
        >
          {t.staff.task.draftHeading}
        </button>
      </h2>
      {open ? (
        <div id={panelId} className={styles.panel} data-testid="ai-draft-panel">
          <DraftBody draft={draft} reasons={reasons} quoteStatus={quoteStatus} language={language} testId="ai-draft" />
        </div>
      ) : null}
    </section>
  );
}

interface DraftBodyProps {
  /** The task's draft, or the part of it a report keeps (no advisory step). */
  draft: ReportAIPart & { readonly advisory_next_step?: string };
  /** AI-derived and unknown review reasons: shown only with the draft. */
  reasons: readonly string[];
  /** Whether each quote was found in the transcript shown; absent where no transcript is shown. */
  quoteStatus?: readonly QuoteStatus[];
  language: string;
  /** Prefix of the scores' and reasons' test ids. */
  testId?: string;
}

/** What an AI draft says, always marked as an uncalibrated draft: on the task page and in the report. */
export function DraftBody({ draft, reasons, quoteStatus, language, testId }: DraftBodyProps) {
  const t = useMessages();
  return (
    <>
      <p className={ui.warning}>{t.staff.task.draftNote}</p>
      <h3>{t.staff.task.draftScores}</h3>
      <ScoresTable scores={draft.scores} testId={testId === undefined ? undefined : `${testId}-scores`} />
      <p>{t.staff.task.draftWeighted(formatPoints(draft.weighted_score))}</p>
      <p className={styles.meta}>
        {t.staff.task.draftConfidence(draft.confidence.toFixed(2))} · {t.staff.task.draftEvaluator(draft.evaluator_version)}
      </p>
      {draft.advisory_next_step !== undefined ? <p className={styles.meta}>{t.staff.task.draftAdvisory(draft.advisory_next_step)}</p> : null}
      {reasons.length > 0 ? (
        <>
          <h3>{t.staff.task.draftReasons}</h3>
          <Reasons reasons={reasons} testId={testId === undefined ? undefined : `${testId}-reasons`} />
        </>
      ) : null}
      <h3>{t.staff.task.draftEvidence}</h3>
      <EvidenceList evidence={draft.evidence} quoteStatus={quoteStatus} language={language} />
    </>
  );
}

export function ScoresTable({ scores, testId }: { scores: DimensionScores; testId?: string }) {
  const t = useMessages();
  return (
    <table className={styles.table} data-testid={testId}>
      <tbody>
        {DIMENSIONS.map((d) => (
          <tr key={d}>
            <th scope="row">{t.staff.dimensions[d]}</th>
            <td data-dimension={d}>{scores[d]}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function EvidenceList({
  evidence,
  quoteStatus,
  language,
}: {
  evidence: readonly EvidenceItem[];
  quoteStatus?: readonly QuoteStatus[];
  language: string;
}) {
  const t = useMessages();
  return (
    <ul className={styles.evidence}>
      {evidence.map((item, i) => (
        <li key={i} className={styles.evidenceItem}>
          <p>
            <strong>{t.staff.dimensions[item.dimension]}</strong> · {t.staff.assessments[item.assessment]}
          </p>
          {item.assessment === 'not_addressed' || item.quote.trim() === '' ? (
            <p className={styles.meta}>{t.staff.task.noQuote}</p>
          ) : (
            <blockquote className={styles.quote} lang={language}>
              {item.quote}
            </blockquote>
          )}
          {item.claim !== '' ? <p>{item.claim}</p> : null}
          {quoteStatus?.[i] === 'not_located' ? <p className={styles.fieldError}>{t.staff.task.notLocated}</p> : null}
        </li>
      ))}
    </ul>
  );
}
