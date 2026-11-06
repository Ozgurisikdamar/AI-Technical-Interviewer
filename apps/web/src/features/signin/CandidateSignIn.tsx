'use client';

import { useRouter } from 'next/navigation';
import { useId, useRef, useState, useSyncExternalStore, type FormEvent, type ReactNode } from 'react';
import { ErrorNotice } from '@/components/ErrorNotice';
import ui from '@/components/ui.module.css';
import { useFocusOnMount } from '@/components/useFocusOnMount';
import { useMessages } from '@/i18n/client';
import { toApiError, type ApiError } from '@/lib/api/errors';
import { signInCandidate } from '@/lib/api/interviews';
import { normalizeCode, normalizeNumber } from '@/lib/signin/codes';
import styles from './CandidateSignIn.module.css';

const noSubscription = () => () => {};

type FieldProblem = 'required' | 'malformed';

interface Problems {
  number: FieldProblem | null;
  code: FieldProblem | null;
}

/**
 * Candidate sign-in with the application number and the access code (ADR-024).
 *
 * Both inputs are uncontrolled and have no `name`, so neither value can reach an address or a
 * request body even if the form were submitted before the page hydrated (the inputs and the
 * button are disabled until then). The values are read only on submit and never put in state or
 * storage; the code is cleared once the server accepts it. The code field is a password field
 * that can be shown, and pasting into it is allowed (WCAG 2.2, SC 3.3.8). What the server would
 * refuse for its format is refused here, without a request. Every refusal from the server reads
 * the same, whatever its reason.
 */
export function CandidateSignIn() {
  const t = useMessages();
  const router = useRouter();
  const hydrated = useSyncExternalStore(noSubscription, () => true, () => false);
  const numberRef = useRef<HTMLInputElement | null>(null);
  const codeRef = useRef<HTMLInputElement | null>(null);
  const id = useId();
  const [problems, setProblems] = useState<Problems | null>(null);
  const [failure, setFailure] = useState<ApiError | null>(null);
  const [attempts, setAttempts] = useState(0);
  const [pending, setPending] = useState(false);
  const [showCode, setShowCode] = useState(false);
  const s = t.signIn;

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending || !hydrated) return;
    const typedNumber = numberRef.current?.value ?? '';
    const typedCode = codeRef.current?.value ?? '';
    const number = normalizeNumber(typedNumber);
    const code = normalizeCode(typedCode);
    const found: Problems = {
      number: number !== null ? null : typedNumber.trim() === '' ? 'required' : 'malformed',
      code: code !== null ? null : typedCode.trim() === '' ? 'required' : 'malformed',
    };
    setFailure(null);
    if (number === null || code === null) {
      setProblems(found);
      setAttempts((n) => n + 1);
      return;
    }
    setProblems(null);
    setPending(true);
    try {
      const signedIn = await signInCandidate(number, code);
      if (codeRef.current !== null) codeRef.current.value = '';
      // Stays pending: the interview page replaces this one.
      router.replace(`/interview/${encodeURIComponent(signedIn.interview_id)}`);
    } catch (error) {
      setFailure(toApiError(error));
      setAttempts((n) => n + 1);
      setPending(false);
    }
  }

  const refused = failure !== null && failure.status === 401;
  const numberInvalid = (problems !== null && problems.number !== null) || refused;
  const codeInvalid = (problems !== null && problems.code !== null) || refused;
  const problemId = `${id}-problem`;
  const describedBy = (hint: string, invalid: boolean) => [hint, invalid ? problemId : null].filter(Boolean).join(' ');
  const numberMessage = { required: s.numberRequired, malformed: s.numberMalformed };
  const codeMessage = { required: s.codeRequired, malformed: s.codeMalformed };

  return (
    <section aria-labelledby={`${id}-heading`} className={styles.signIn}>
      <h2 id={`${id}-heading`}>{s.heading}</h2>
      <p className={ui.muted}>{s.intro}</p>
      {problems !== null || failure !== null ? (
        // A new block on every attempt, so each one takes focus.
        <Problem key={attempts} id={problemId}>
          {problems !== null ? (
            <div className={ui.error} role="alert">
              <ul className={styles.problems}>
                {problems.number !== null ? <li>{numberMessage[problems.number]}</li> : null}
                {problems.code !== null ? <li>{codeMessage[problems.code]}</li> : null}
              </ul>
            </div>
          ) : refused ? (
            <div className={ui.error} role="alert">
              <p>{s.refused}</p>
              <p>{s.lockNote}</p>
            </div>
          ) : failure !== null ? (
            <ErrorNotice error={failure} />
          ) : null}
        </Problem>
      ) : null}
      <form method="post" onSubmit={(e) => void onSubmit(e)} noValidate className={styles.form}>
        <div className={styles.field}>
          <label htmlFor={`${id}-number`} className={styles.label}>
            {s.numberLabel}
          </label>
          <p id={`${id}-number-hint`} className={styles.hint}>
            {s.numberHint}
          </p>
          <input
            ref={numberRef}
            id={`${id}-number`}
            type="text"
            className={`${styles.input} ${styles.number}`}
            autoComplete="off"
            autoCapitalize="characters"
            autoCorrect="off"
            spellCheck={false}
            disabled={!hydrated || pending}
            aria-invalid={numberInvalid ? true : undefined}
            aria-describedby={describedBy(`${id}-number-hint`, numberInvalid)}
            data-testid="sign-in-number"
          />
        </div>
        <div className={styles.field}>
          <label htmlFor={`${id}-code`} className={styles.label}>
            {s.codeLabel}
          </label>
          <p id={`${id}-code-hint`} className={styles.hint}>
            {s.codeHint}
          </p>
          <div className={styles.codeRow}>
            <input
              ref={codeRef}
              id={`${id}-code`}
              type={showCode ? 'text' : 'password'}
              className={`${styles.input} ${styles.code}`}
              autoComplete="off"
              autoCapitalize="characters"
              autoCorrect="off"
              spellCheck={false}
              disabled={!hydrated || pending}
              aria-invalid={codeInvalid ? true : undefined}
              aria-describedby={describedBy(`${id}-code-hint`, codeInvalid)}
              data-testid="sign-in-code"
            />
            <button
              type="button"
              className={`${ui.button} ${ui.secondary} ${styles.reveal}`}
              aria-pressed={showCode}
              aria-controls={`${id}-code`}
              disabled={!hydrated || pending}
              onClick={() => setShowCode((shown) => !shown)}
              data-testid="sign-in-show-code"
            >
              {s.showCode}
            </button>
          </div>
        </div>
        <div className={ui.actions}>
          <button
            type="submit"
            className={`${ui.button} ${ui.primary} ${styles.submit}`}
            disabled={!hydrated || pending}
            aria-busy={pending}
            data-testid="sign-in-submit"
          >
            {pending ? s.submitting : s.submit}
          </button>
        </div>
      </form>
    </section>
  );
}

function Problem({ id, children }: { id: string; children: ReactNode }) {
  const ref = useFocusOnMount<HTMLDivElement>(true);
  return (
    <div ref={ref} tabIndex={-1} id={id} className={styles.problem} data-testid="sign-in-problem">
      {children}
    </div>
  );
}
