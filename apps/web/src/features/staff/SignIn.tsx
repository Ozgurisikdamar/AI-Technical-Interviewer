'use client';

import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useEffect, useId, useRef, useState, useSyncExternalStore, type FormEvent } from 'react';
import { ErrorNotice } from '@/components/ErrorNotice';
import ui from '@/components/ui.module.css';
import { useMessages } from '@/i18n/client';
import { toApiError, type ApiError } from '@/lib/api/errors';
import { signIn } from '@/lib/api/staff';
import { homeFor } from '@/lib/staff/nav';
import { cleanToken, isStaffTokenFormat } from '@/lib/staff/token';
import styles from './staff.module.css';
import { useStaffSession } from './StaffSession';

const noSubscription = () => () => {};

/**
 * Sign-in. The token is read from an uncontrolled input that has no `name`, so it cannot reach a
 * URL or a request body even if the form were submitted before the page hydrated (the input and
 * the button are disabled until then). It is cleared once the server accepts it, and is never put
 * in state, storage or an address.
 */
export function SignIn() {
  const t = useMessages();
  const router = useRouter();
  const { state, signedIn } = useStaffSession();
  const hydrated = useSyncExternalStore(noSubscription, () => true, () => false);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const problemRef = useRef<HTMLDivElement | null>(null);
  const id = useId();
  const [problem, setProblem] = useState<'required' | 'malformed' | null>(null);
  const [failure, setFailure] = useState<ApiError | null>(null);
  const [attempts, setAttempts] = useState(0);
  const [pending, setPending] = useState(false);

  useEffect(() => {
    if (attempts > 0) problemRef.current?.focus();
  }, [attempts]);

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending || !hydrated) return;
    const input = inputRef.current;
    const token = cleanToken(input?.value ?? '');
    setFailure(null);
    if (token === '' || !isStaffTokenFormat(token)) {
      setProblem(token === '' ? 'required' : 'malformed');
      setAttempts((n) => n + 1);
      return;
    }
    setProblem(null);
    setPending(true);
    try {
      const session = await signIn(token);
      if (input !== null) input.value = '';
      signedIn(session);
      router.push(homeFor(session.role));
    } catch (error) {
      setFailure(toApiError(error));
      setAttempts((n) => n + 1);
    } finally {
      setPending(false);
    }
  }

  const signedInSession = state.status === 'signed_in' ? state.session : null;
  const refused = failure !== null && failure.status === 401;
  const describedBy = [`${id}-hint`, problem !== null || refused ? `${id}-problem` : null].filter(Boolean).join(' ');

  return (
    <div className={ui.page}>
      <h1>{t.staff.signIn.heading}</h1>
      {state.status === 'signed_out' && state.reason === 'signed_out' ? (
        <p role="status" className={ui.info} data-testid="staff-signed-out">
          {t.staff.signIn.signedOut}
        </p>
      ) : null}
      {state.status === 'signed_out' && state.reason === 'ended' ? <p className={ui.warning}>{t.staff.signIn.sessionEnded}</p> : null}
      {signedInSession !== null ? (
        <div className={ui.info}>
          <p>{t.staff.signIn.signedIn(signedInSession.label)}</p>
          <p>
            <Link href={homeFor(signedInSession.role)}>{t.staff.signIn.continue}</Link>
          </p>
        </div>
      ) : (
        <>
          <p>{t.staff.signIn.intro}</p>
          {problem !== null || failure !== null ? (
            <div ref={problemRef} tabIndex={-1} id={`${id}-problem`} data-testid="staff-sign-in-problem">
              {problem !== null ? (
                <p role="alert" className={ui.error}>
                  {problem === 'required' ? t.staff.signIn.required : t.staff.signIn.malformed}
                </p>
              ) : refused ? (
                <p role="alert" className={ui.error}>
                  {t.staff.signIn.refused}
                </p>
              ) : failure !== null ? (
                <ErrorNotice error={failure} catalog={t.staff.errors} />
              ) : null}
            </div>
          ) : null}
          <form method="post" onSubmit={(e) => void onSubmit(e)} noValidate className={styles.form}>
            <div className={styles.field}>
              <label htmlFor={`${id}-token`} className={styles.label}>
                {t.staff.signIn.label}
              </label>
              <p id={`${id}-hint`} className={styles.hint}>
                {t.staff.signIn.hint}
              </p>
              <input
                ref={inputRef}
                id={`${id}-token`}
                type="password"
                className={`${styles.input} ${styles.tokenInput}`}
                autoComplete="off"
                autoCapitalize="off"
                autoCorrect="off"
                spellCheck={false}
                disabled={!hydrated || pending}
                aria-invalid={problem !== null || refused ? true : undefined}
                aria-describedby={describedBy}
                data-testid="staff-token"
              />
            </div>
            <div className={ui.actions}>
              <button
                type="submit"
                className={`${ui.button} ${ui.primary}`}
                disabled={!hydrated || pending}
                aria-busy={pending}
                data-testid="staff-sign-in"
              >
                {pending ? t.staff.signIn.submitting : t.staff.signIn.submit}
              </button>
            </div>
          </form>
        </>
      )}
    </div>
  );
}
