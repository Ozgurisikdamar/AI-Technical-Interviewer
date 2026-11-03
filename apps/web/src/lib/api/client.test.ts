import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { getApiEnvironment, resetApiEnvironmentForTests } from '@/lib/environment';
import { assertApiPath, getJson, newIdempotencyKey, postJson, problemFromResponse } from './client';
import { ApiError, isAbortError } from './errors';
import * as interviews from './interviews';

const ID = 'ses_ABCDEFGHIJKLMNOPQRSTUV';
const ACT = 'act_ABCDEFGHIJKLMNOPQRSTUV';
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

const candidateView = {
  id: ID,
  state: 'QUESTION_ACTIVE',
  state_version: 7,
  progress: { completed_items: 1, total_items: 4 },
  input_mode: 'voice',
  active_prompt: { activation_id: ACT, kind: 'question', text: 'Describe a cache you designed.' },
  paused_from: null,
  pending_completion: false,
  recording_active: false,
  allowed_actions: ['pause', 'finish_answer', 'withdraw', 'join_media'],
  environment: 'dev',
};

function json(body: unknown, init: ResponseInit = {}): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    ...init,
    headers: { 'Content-Type': 'application/json', ...(init.headers ?? {}) },
  });
}

function problem(status: number, body: Record<string, unknown>, headers: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/problem+json', ...headers },
  });
}

type FetchArgs = [input: string, init: RequestInit];

function mockFetch(respond: (input: string, init: RequestInit) => Response | Promise<Response>) {
  const fn = vi.fn((input: string, init: RequestInit) => Promise.resolve(respond(input, init)));
  vi.stubGlobal('fetch', fn);
  return fn;
}

function headersOf(init: RequestInit): Record<string, string> {
  return init.headers as Record<string, string>;
}

beforeEach(() => resetApiEnvironmentForTests());
afterEach(() => vi.useRealTimers());

describe('POST requests', () => {
  it('send a fresh Idempotency-Key and JSON content type on every call', async () => {
    const fetchMock = mockFetch(() => json(candidateView));
    await interviews.pauseInterview(ID, 7, 'candidate_request');
    await interviews.pauseInterview(ID, 7, 'candidate_request');

    const [first, second] = fetchMock.mock.calls as unknown as FetchArgs[];
    const h1 = headersOf(first![1]);
    const h2 = headersOf(second![1]);
    expect(first![1].method).toBe('POST');
    expect(h1['Content-Type']).toBe('application/json');
    expect(h1['Idempotency-Key']).toMatch(UUID);
    expect(h1['Idempotency-Key']).toMatch(/^[A-Za-z0-9_.:-]{8,128}$/);
    expect(h2['Idempotency-Key']).not.toBe(h1['Idempotency-Key']);
    expect(JSON.parse(first![1].body as string)).toEqual({ state_version: 7, reason: 'candidate_request' });
  });

  it('use same-origin credentials and mode, no caching and no redirects', async () => {
    const fetchMock = mockFetch(() => json(candidateView));
    await postJson(`/api/v1/interviews/${ID}/resume`, { state_version: 3 }, (b) => b);
    const init = (fetchMock.mock.calls[0] as unknown as FetchArgs)[1];
    expect(init.credentials).toBe('same-origin');
    expect(init.mode).toBe('same-origin');
    expect(init.cache).toBe('no-store');
    expect(init.redirect).toBe('error');
    expect(init.referrerPolicy).toBe('no-referrer');
  });

  it('send {} as the JSON body for endpoints without a request body', async () => {
    const fetchMock = mockFetch(() =>
      json({
        livekit_url: 'ws://localhost:7880',
        token: 'test-only-livekit-token',
        room: 'rm_x',
        identity: 'candidate',
        can_publish_audio: true,
        expires_at: '2026-11-03T12:00:00Z',
      }),
    );
    await interviews.issueRealtimeSession(ID);
    const init = (fetchMock.mock.calls[0] as unknown as FetchArgs)[1];
    expect(init.body).toBe('{}');
    expect(headersOf(init)['Content-Type']).toBe('application/json');
  });

  it('falls back to getRandomValues when randomUUID is unavailable (insecure context)', () => {
    const original = globalThis.crypto;
    vi.stubGlobal('crypto', { getRandomValues: (a: Uint8Array) => original.getRandomValues(a) });
    expect(newIdempotencyKey()).toMatch(UUID);
  });
});

describe('GET requests', () => {
  it('carry no Idempotency-Key or Content-Type', async () => {
    const fetchMock = mockFetch(() => json(candidateView));
    const view = await interviews.getInterview(ID);
    const init = (fetchMock.mock.calls[0] as unknown as FetchArgs)[1];
    expect(init.method).toBe('GET');
    expect(headersOf(init)['Idempotency-Key']).toBeUndefined();
    expect(headersOf(init)['Content-Type']).toBeUndefined();
    expect(init.body).toBeUndefined();
    expect(view.state).toBe('QUESTION_ACTIVE');
  });
});

describe('relative /api/v1 URLs only', () => {
  it('every endpoint calls a relative public path', async () => {
    const urls: string[] = [];
    mockFetch((input) => {
      urls.push(input);
      if (input.endsWith('/realtime-session')) {
        return json({
          livekit_url: 'wss://lk.example.invalid',
          token: 'test-only-livekit-token',
          room: 'rm_x',
          identity: 'candidate',
          can_publish_audio: false,
          expires_at: '2026-11-03T12:00:00Z',
        });
      }
      if (input.endsWith('/text-responses')) return json({ segment_id: 'seg_1', state: 'CANDIDATE_SPEAKING', state_version: 9 }, { status: 201 });
      if (input.endsWith('/exchange')) return json({ interview_id: ID, state: 'CONSENT_PENDING' });
      if (input.endsWith('/consent-notice')) {
        return json({
          notice: {
            notice_id: 'notice_default',
            version: '2026-11-draft.1',
            status: 'draft',
            language: 'en',
            title: 'T',
            sections: [{ heading: 'H', text: 'X' }],
            purposes: [
              {
                purpose_id: 'transcript_assessment_record',
                required: true,
                basis: 'to_be_determined_by_legal',
                title: 'P',
                description: 'D',
                data_categories: ['transcript'],
                retention_category: 'transcript',
              },
            ],
          },
          retention_days: { transcript: 30, session_events: 30, assessment_output: 30, audio_recording: null },
        });
      }
      return json(candidateView);
    });

    await interviews.exchangeInvitation('test-only-0123456789abcdefghijklmnopqrstu');
    await interviews.getInterview(ID);
    await interviews.getConsentNotice(ID);
    await interviews.submitConsent(ID, { notice_id: 'n', notice_version: 'v', decisions: [] });
    await interviews.startInterview(ID, 1, 'text');
    await interviews.completeCalibration(ID, 2);
    await interviews.pauseInterview(ID, 3, 'device_lost');
    await interviews.resumeInterview(ID, 4);
    await interviews.requestAnswerCompletion(ID, 5, ACT);
    await interviews.withdrawInterview(ID, 6);
    await interviews.issueRealtimeSession(ID);
    await interviews.submitTextResponse(ID, ACT, 'An answer');

    expect(urls).toHaveLength(12);
    for (const url of urls) {
      expect(url.startsWith('/api/v1/')).toBe(true);
      expect(url).not.toMatch(/^[a-z]+:/i);
      expect(url).not.toMatch(/^\/\//);
      expect(url).not.toMatch(/internal/i);
    }
    expect(urls).toContain('/api/v1/invitations/exchange');
    expect(urls).toContain(`/api/v1/interviews/${ID}/answer-complete`);
  });

  it.each([
    'https://evil.example.invalid/api/v1/interviews',
    '//evil.example.invalid/api/v1/interviews',
    'api/v1/interviews',
    '/api/v2/interviews',
    '/internal/v1/tier1/claims',
    '/api/v1/../internal/v1/tier1/claims',
    '/api/v1/./interviews',
    '/api/v1/%2e%2e/internal/v1/x',
    '/api/v1/interviews?x=1',
    '/api/v1/interviews#frag',
    '/api/v1//interviews',
    '/api/v1/internal/x',
    '/api/v1\\interviews',
  ])('refuses %s before any request is made', async (path) => {
    const fetchMock = mockFetch(() => json({}));
    expect(() => assertApiPath(path)).toThrow();
    await expect(getJson(path, (b) => b)).rejects.toThrow();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('refuses malformed identifiers instead of building a path', () => {
    expect(() => interviews.interviewPath('../internal')).toThrow();
    expect(() => interviews.interviewPath('ses_short')).toThrow();
    expect(interviews.interviewPath(ID, '/pause')).toBe(`/api/v1/interviews/${ID}/pause`);
  });
});

describe('problem+json errors', () => {
  it('parses the stable code and request id, never the title or detail', async () => {
    mockFetch(() =>
      problem(409, {
        type: 'https://example.invalid/problems/stale_state_version',
        title: 'Conflict',
        status: 409,
        detail: '<img src=x onerror=alert(1)> user supplied text',
        code: 'stale_state_version',
        request_id: 'req_0123456789abcdefghij',
      }),
    );
    const error = await interviews.resumeInterview(ID, 1).catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ApiError);
    const apiError = error as ApiError;
    expect(apiError.status).toBe(409);
    expect(apiError.code).toBe('stale_state_version');
    expect(apiError.requestId).toBe('req_0123456789abcdefghij');
    expect(JSON.stringify(apiError)).not.toContain('onerror');
    expect(apiError.message).not.toContain('user supplied');
    expect(Object.values(apiError)).not.toContain('Conflict');
  });

  it('maps malformed codes to "unknown" and keeps a header request id', async () => {
    mockFetch(() => problem(400, { code: 'Bad Code; DROP', title: 'x', status: 400 }, { 'X-Request-ID': 'req_header_12345678' }));
    const error = (await getJson('/api/v1/interviews/x', (b) => b).catch((e: unknown) => e)) as ApiError;
    expect(error.code).toBe('unknown');
    expect(error.requestId).toBe('req_header_12345678');
  });

  it('handles non-JSON error bodies (e.g. an HTML 502 from a proxy)', async () => {
    mockFetch(() => new Response('<html>Bad gateway</html>', { status: 502, headers: { 'Content-Type': 'text/html' } }));
    const error = (await getJson('/api/v1/interviews/x', (b) => b).catch((e: unknown) => e)) as ApiError;
    expect(error).toBeInstanceOf(ApiError);
    expect(error.status).toBe(502);
    expect(error.code).toBe('unknown');
  });

  it('handles an unparseable problem body', async () => {
    const response = new Response('{not json', { status: 500, headers: { 'Content-Type': 'application/problem+json' } });
    const error = await problemFromResponse(response);
    expect(error.status).toBe(500);
    expect(error.code).toBe('unknown');
  });

  it('rejects malformed request ids', () => {
    expect(new ApiError(500, 'internal_error', 'bad id with spaces').requestId).toBeNull();
  });
});

describe('transport failures', () => {
  it('reports network failures as network_error with status 0', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError('Failed to fetch'))));
    await expect(getJson('/api/v1/interviews/x', (b) => b)).rejects.toMatchObject({ status: 0, code: 'network_error' });
  });

  it('times out', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(
        (_input: string, init: RequestInit) =>
          new Promise((_resolve, reject) => {
            init.signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')));
          }),
      ),
    );
    await expect(getJson('/api/v1/interviews/x', (b) => b, { timeoutMs: 5 })).rejects.toMatchObject({ code: 'timeout' });
  });

  it('propagates caller aborts as AbortError', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(
        (_input: string, init: RequestInit) =>
          new Promise((_resolve, reject) => {
            init.signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')));
          }),
      ),
    );
    const controller = new AbortController();
    const pending = getJson('/api/v1/interviews/x', (b) => b, { signal: controller.signal });
    controller.abort();
    const error = await pending.catch((e: unknown) => e);
    expect(isAbortError(error)).toBe(true);
  });

  it('turns a contract-violating success body into invalid_response', async () => {
    mockFetch(() => json({ ...candidateView, state_version: 'seven' }));
    await expect(interviews.getInterview(ID)).rejects.toMatchObject({ code: 'invalid_response', status: 200 });
  });
});

describe('environment reporting', () => {
  it('records X-ATI-Environment from any response', async () => {
    mockFetch(() => json(candidateView, { headers: { 'X-ATI-Environment': 'staging' } }));
    await interviews.getInterview(ID);
    expect(getApiEnvironment()).toBe('staging');
  });
});
