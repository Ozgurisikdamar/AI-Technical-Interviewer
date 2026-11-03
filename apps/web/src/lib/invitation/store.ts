/**
 * In-memory holder for the invitation token. The token lives in a module variable only: never in
 * React state or props, browser storage, cookies, logs or the URL once captured.
 * Exposed as an external store for useSyncExternalStore; capture runs when the first subscriber
 * attaches (after hydration), not during render.
 */
import { captureInvitation, stripInvitationFromAddressBar } from './fragment';

export type InvitationProblem = 'missing' | 'malformed' | 'query_token' | 'consumed';

export type InvitationSnapshot =
  | { readonly status: 'pending' }
  | { readonly status: 'ready'; readonly hasToken: true; readonly problem: null }
  | { readonly status: 'ready'; readonly hasToken: false; readonly problem: InvitationProblem };

const PENDING: InvitationSnapshot = { status: 'pending' };

let snapshot: InvitationSnapshot = PENDING;
let token: string | null = null;
const listeners = new Set<() => void>();
let syncTimer: ReturnType<typeof setTimeout> | null = null;

function emit(): void {
  for (const listener of listeners) listener();
}

function capture(): void {
  if (typeof window === 'undefined') return;
  const result = captureInvitation(window.location, window.history);
  if (result.kind === 'missing' && snapshot.status === 'ready') {
    // A hashchange that did not carry a new token keeps what we already hold.
    return;
  }
  token = result.kind === 'token' ? result.token : null;
  snapshot =
    result.kind === 'token'
      ? { status: 'ready', hasToken: true, problem: null }
      : { status: 'ready', hasToken: false, problem: result.kind };
  // Strip once more after the current task: by then the Next.js router has installed its
  // history integration, so its own copy of the URL (still holding the fragment) is replaced too.
  if (syncTimer !== null) clearTimeout(syncTimer);
  syncTimer = setTimeout(() => {
    syncTimer = null;
    stripInvitationFromAddressBar(window.location, window.history);
  }, 0);
  emit();
}

function onHashChange(): void {
  if (window.location.hash.length > 1) capture();
}

export function subscribeInvitation(listener: () => void): () => void {
  listeners.add(listener);
  if (listeners.size === 1) window.addEventListener('hashchange', onHashChange);
  if (snapshot.status === 'pending') capture();
  return () => {
    listeners.delete(listener);
    if (listeners.size === 0) window.removeEventListener('hashchange', onHashChange);
  };
}

export function getInvitationSnapshot(): InvitationSnapshot {
  return snapshot;
}

export function getServerInvitationSnapshot(): InvitationSnapshot {
  return PENDING;
}

/** The captured token, for the explicit "Continue" click only. */
export function peekInvitationToken(): string | null {
  return token;
}

/** Drops the token after a successful exchange or a definitive refusal. */
export function forgetInvitationToken(): void {
  token = null;
  snapshot = { status: 'ready', hasToken: false, problem: 'consumed' };
  emit();
}
