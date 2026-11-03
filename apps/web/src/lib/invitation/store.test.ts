import { beforeEach, describe, expect, it, vi } from 'vitest';

const TOKEN = 'test-only-QbZ3k9vX2mN8pL4rT6wY1uE5oI7aS0dF';

interface FakeWindow {
  location: { pathname: string; search: string; hash: string };
  history: { replaceState: (data: unknown, unused: string, url?: string | null) => void };
  addEventListener: (type: string, listener: () => void) => void;
  removeEventListener: (type: string, listener: () => void) => void;
  fire: (type: string) => void;
  replaceCalls: Array<[unknown, string, string | null | undefined]>;
}

function installWindow(hash: string): FakeWindow {
  const listeners = new Map<string, Set<() => void>>();
  const win: FakeWindow = {
    location: { pathname: '/i', search: '', hash },
    replaceCalls: [],
    history: {
      replaceState(data, unused, url) {
        win.replaceCalls.push([data, unused, url]);
        // Like a browser: the new URL replaces the address (and its fragment).
        if (typeof url === 'string') {
          const parsed = new URL(url, 'https://candidate.example.invalid');
          win.location.pathname = parsed.pathname;
          win.location.search = parsed.search;
          win.location.hash = parsed.hash;
        }
      },
    },
    addEventListener(type, listener) {
      if (!listeners.has(type)) listeners.set(type, new Set());
      listeners.get(type)?.add(listener);
    },
    removeEventListener(type, listener) {
      listeners.get(type)?.delete(listener);
    },
    fire(type) {
      for (const listener of listeners.get(type) ?? []) listener();
    },
  };
  vi.stubGlobal('window', win);
  return win;
}

async function freshStore() {
  vi.resetModules();
  return import('./store');
}

beforeEach(() => {
  vi.useFakeTimers();
  return () => vi.useRealTimers();
});

describe('invitation token store', () => {
  it('is pending until the first subscriber attaches (no capture during render)', async () => {
    const win = installWindow(`#token=${TOKEN}`);
    const store = await freshStore();
    expect(store.getInvitationSnapshot().status).toBe('pending');
    expect(store.getServerInvitationSnapshot().status).toBe('pending');
    expect(win.replaceCalls).toHaveLength(0);
  });

  it('captures the token on subscribe, strips the address bar and keeps the token only in memory', async () => {
    const win = installWindow(`#token=${TOKEN}`);
    const store = await freshStore();
    const listener = vi.fn();
    const unsubscribe = store.subscribeInvitation(listener);

    expect(store.getInvitationSnapshot()).toEqual({ status: 'ready', hasToken: true, problem: null });
    expect(JSON.stringify(store.getInvitationSnapshot())).not.toContain(TOKEN);
    expect(win.location.hash).toBe('');
    expect(win.replaceCalls[0]).toEqual([null, '', '/i']);
    expect(store.peekInvitationToken()).toBe(TOKEN);
    expect(listener).toHaveBeenCalled();

    // Second pass after the router's history integration is installed.
    vi.runAllTimers();
    expect(win.replaceCalls).toHaveLength(2);
    expect(win.replaceCalls.every(([, , url]) => !String(url).includes(TOKEN))).toBe(true);
    unsubscribe();
  });

  it('forgets the token after use', async () => {
    installWindow(`#token=${TOKEN}`);
    const store = await freshStore();
    store.subscribeInvitation(() => undefined);
    store.forgetInvitationToken();
    expect(store.peekInvitationToken()).toBeNull();
    expect(store.getInvitationSnapshot()).toEqual({ status: 'ready', hasToken: false, problem: 'consumed' });
  });

  it('reports a missing token without touching history', async () => {
    const win = installWindow('');
    const store = await freshStore();
    store.subscribeInvitation(() => undefined);
    expect(store.getInvitationSnapshot()).toEqual({ status: 'ready', hasToken: false, problem: 'missing' });
    expect(store.peekInvitationToken()).toBeNull();
    expect(win.replaceCalls).toHaveLength(0);
  });

  it('captures a new token pasted into the same tab (hashchange) and strips it again', async () => {
    const win = installWindow('');
    const store = await freshStore();
    store.subscribeInvitation(() => undefined);
    win.location.hash = `#token=${TOKEN}`;
    win.fire('hashchange');
    expect(store.peekInvitationToken()).toBe(TOKEN);
    expect(win.location.hash).toBe('');
  });
});
