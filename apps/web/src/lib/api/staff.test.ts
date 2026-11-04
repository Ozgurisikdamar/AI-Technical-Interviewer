import { afterEach, describe, expect, it, vi } from 'vitest';
import { buildQuery } from './client';
import { ApiError } from './errors';
import * as staff from './staff';
import {
  parseReport,
  parseReportList,
  parseReviewOutcomeResult,
  parseReviewTask,
  parseReviewTaskList,
  parseStaffSession,
  safeReferenceUrl,
} from './staffParse';

const SES = 'ses_ABCDEFGHIJKLMNOPQRSTUV';
const TASK = 'rvt_ABCDEFGHIJKLMNOPQRSTUV';
const ANS = 'ans_ABCDEFGHIJKLMNOPQRSTUV';
const SEG = 'seg_ABCDEFGHIJKLMNOPQRSTUV';
const SCORES = { technical_accuracy: 50, architecture_maturity: 50, edge_case_awareness: 50, claim_support: 50 };

const summary = {
  task_id: TASK,
  interview_id: SES,
  answer_id: ANS,
  item_id: 'q1',
  status: 'open',
  reasons: ['policy_all_reviewed', 'brand_new_reason'],
  has_ai_evaluation: true,
  created_at: '2026-11-04T13:13:52.411403Z',
  completed_at: null,
};

const rubric = {
  rubric_id: 'rub_demo_postgres_mvcc',
  version: 1,
  status: 'draft',
  synthetic: true,
  title: 'PostgreSQL MVCC',
  languages: ['en', 'tr'],
  weights: { technical_accuracy: 0.4, architecture_maturity: 0.25, edge_case_awareness: 0.2, claim_support: 0.15 },
  criteria: [{ criterion_id: 'c_row_versions', dimension: 'technical_accuracy', description: 'Explains row versions.', reference_ids: ['ref_pg_mvcc'] }],
  references: [
    { reference_id: 'ref_pg_mvcc', title: 'Concurrency Control', url: 'https://www.postgresql.org/docs/current/mvcc.html', summary: 'Row versions.' },
    { reference_id: 'ref_pg_bad', title: 'Not https', url: 'javascript:alert(1)', summary: 'Dropped.' },
  ],
};

const task = {
  ...summary,
  language: 'en',
  question: 'How does MVCC work?',
  rubric_ref: 'rub_demo_postgres_mvcc@1',
  rubric,
  transcript: [{ segment_id: SEG, start_ms: 97, end_ms: 102, text: 'Each update writes a new row version.' }],
  ai_evaluation: {
    evaluation_id: 'evl_ABCDEFGHIJKLMNOPQRSTUV',
    evaluator_version: 'reference@1',
    scores: SCORES,
    weighted_score: 50,
    confidence: 0.9,
    calibrated: false,
    advisory_next_step: 'NEXT_QUESTION',
    evaluator_output_risk: 'low',
    routing_reasons: ['policy_all_reviewed'],
    evidence: [
      {
        dimension: 'technical_accuracy',
        transcript_start_ms: 97,
        transcript_end_ms: 102,
        transcript_segment_ids: [SEG],
        quote: 'a new row version',
        claim: 'c',
        assessment: 'correct',
        reference_ids: ['ref_pg_mvcc'],
      },
    ],
  },
  outcome: null,
};

function json(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } });
}

afterEach(() => vi.unstubAllGlobals());

describe('query parameters', () => {
  it('are allow-listed and plain', () => {
    expect(buildQuery(undefined)).toBe('');
    expect(buildQuery({ status: 'open', interview_id: SES, limit: 50 })).toBe(`?status=open&interview_id=${SES}&limit=50`);
    expect(buildQuery({ status: 'open', interview_id: undefined })).toBe('?status=open');
    for (const bad of [{ token: 'x' }, { status: 'open&x=1' }, { status: '../x' }, { status: '' }, { interview_id: 'ses x' }]) {
      expect(() => buildQuery(bad)).toThrow();
    }
  });

  it('reach the queue request, appended to a checked path', async () => {
    const fetchMock = vi.fn((_input: string, _init: RequestInit) => Promise.resolve(json({ review_tasks: [summary] })));
    vi.stubGlobal('fetch', fetchMock);
    const tasks = await staff.listReviewTasks({ status: 'open', interviewId: SES });
    expect(tasks).toHaveLength(1);
    expect(fetchMock.mock.calls[0]?.[0]).toBe(`/api/v1/review-tasks?status=open&interview_id=${SES}&limit=200`);
    expect(() => staff.listReviewTasks({ status: 'open', interviewId: 'ses_bad' })).toThrow();
  });

  it('ask for the largest page of reports', async () => {
    const fetchMock = vi.fn((_input: string, _init: RequestInit) => Promise.resolve(json({ reports: [] })));
    vi.stubGlobal('fetch', fetchMock);
    await staff.listReports();
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/reports?limit=200');
  });

  it('every staff call uses a relative public path and the decision carries ai_draft_viewed', async () => {
    const urls: string[] = [];
    const bodies: unknown[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn((input: string, init: RequestInit) => {
        urls.push(input);
        if (init.body !== undefined) bodies.push(JSON.parse(init.body as string));
        if (input.endsWith('/outcome')) return Promise.resolve(json({ outcome_id: 'rvo_ABCDEFGHIJKLMNOPQRSTUV', interview_state: 'HUMAN_REVIEW', open_review_tasks: 3 }));
        if (input.endsWith('/end')) return Promise.resolve(json({ ended: true }));
        return Promise.resolve(json({ role: 'reviewer', label: 'r', expires_at: '2026-11-04T21:00:00Z' }));
      }),
    );
    await staff.signIn(`ati_st_${'A'.repeat(43)}`);
    await staff.getStaffSession();
    await staff.signOut();
    await staff.submitReviewOutcome(TASK, { scores: SCORES, rationale: 'Accurate but brief.', ai_draft_viewed: false });
    expect(urls).toEqual(['/api/v1/staff/session', '/api/v1/staff/session', '/api/v1/staff/session/end', `/api/v1/review-tasks/${TASK}/outcome`]);
    expect(bodies).toEqual([{ token: `ati_st_${'A'.repeat(43)}` }, {}, { scores: SCORES, rationale: 'Accurate but brief.', ai_draft_viewed: false }]);
    expect(() => staff.getReviewTask('rvt_../../x')).toThrow();
    expect(() => staff.getReport('ses_bad')).toThrow();
  });

  it('turns a contract-violating body into invalid_response', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(json({ role: 'superuser', label: 'x', expires_at: '2026-11-04T21:00:00Z' }))));
    await expect(staff.getStaffSession()).rejects.toEqual(new ApiError(200, 'invalid_response'));
  });
});

describe('staff parsers', () => {
  it('parse the session and refuse unknown roles', () => {
    expect(parseStaffSession({ role: 'recruiter', label: 'r', expires_at: '2026-11-04T21:00:00Z' }).role).toBe('recruiter');
    expect(() => parseStaffSession({ role: 'owner', label: 'r', expires_at: '2026-11-04T21:00:00Z' })).toThrow();
    expect(() => parseStaffSession({ role: 'reviewer', label: 'r', expires_at: 'tomorrow' })).toThrow();
  });

  it('keep unknown reasons once each and refuse malformed ones', () => {
    expect(parseReviewTaskList({ review_tasks: [summary] })[0]?.reasons).toEqual(['policy_all_reviewed', 'brand_new_reason']);
    expect(parseReviewTaskList({ review_tasks: [{ ...summary, reasons: ['no_rubric', 'no_rubric'] }] })[0]?.reasons).toEqual(['no_rubric']);
    expect(() => parseReviewTaskList({ review_tasks: [{ ...summary, reasons: ['Not A Code'] }] })).toThrow();
    expect(() => parseReviewTaskList({ review_tasks: [{ ...summary, task_id: 'rvt_x' }] })).toThrow();
    expect(() => parseReviewTaskList({ review_tasks: [{ ...summary, interview_id: 'ses_<script>' }] })).toThrow();
  });

  it('accept every item id the plan contract allows, and only those', () => {
    for (const ok of ['q1', '1', '2b_follow_up', 'a'.repeat(32)]) {
      expect(parseReviewTaskList({ review_tasks: [{ ...summary, item_id: ok }] })[0]?.item_id, ok).toBe(ok);
    }
    for (const bad of ['', 'Q1', 'q-1', 'a'.repeat(33)]) {
      expect(() => parseReviewTaskList({ review_tasks: [{ ...summary, item_id: bad }] }), bad).toThrow();
    }
  });

  it('parse a task, dropping reference URLs that are not https', () => {
    const parsed = parseReviewTask(task);
    expect(parsed.rubric_ref).toBe('rub_demo_postgres_mvcc@1');
    expect(parsed.rubric?.references.map((r) => r.url)).toEqual(['https://www.postgresql.org/docs/current/mvcc.html', null]);
    expect(parsed.ai_evaluation?.evidence[0]?.quote).toBe('a new row version');
    expect(parseReviewTask({ ...task, rubric_ref: null, rubric: null, ai_evaluation: null }).rubric).toBeNull();
    expect(() => parseReviewTask({ ...task, rubric_ref: 'rub_demo_postgres_mvcc' })).toThrow();
    expect(() => parseReviewTask({ ...task, rubric_ref: undefined })).toThrow();
  });

  it('count lengths in code points, as the server does', () => {
    const outcome = (rationale: string) => ({
      ...task,
      outcome: {
        outcome_id: 'rvo_ABCDEFGHIJKLMNOPQRSTUV',
        reviewer_id: 'stf_ABCDEFGHIJKLMNOPQRSTUV',
        scores: SCORES,
        weighted_score: 50,
        rationale,
        ai_draft_viewed: false,
        decided_at: '2026-11-04T13:13:52Z',
      },
    });
    // 4,000 characters outside the BMP are 8,000 UTF-16 code units.
    expect(parseReviewTask(outcome('😀'.repeat(4_000))).outcome?.rationale).toHaveLength(8_000);
    expect(() => parseReviewTask(outcome('😀'.repeat(4_001)))).toThrow();
    expect(() => parseReviewTask(outcome('x'.repeat(4_001)))).toThrow();
  });

  it('turn an interview state this build does not know into UNKNOWN', () => {
    const result = (interview_state: unknown) => ({ outcome_id: 'rvo_ABCDEFGHIJKLMNOPQRSTUV', interview_state, open_review_tasks: 0 });
    expect(parseReviewOutcomeResult(result('REPORT_READY')).interview_state).toBe('REPORT_READY');
    expect(parseReviewOutcomeResult(result('A_LATER_STATE')).interview_state).toBe('UNKNOWN');
    expect(() => parseReviewOutcomeResult(result('report_ready'))).toThrow();
  });

  it('refuse out-of-range or incomplete scores', () => {
    const withScores = (scores: unknown) => ({ ...task, ai_evaluation: { ...task.ai_evaluation, scores } });
    expect(() => parseReviewTask(withScores({ ...SCORES, claim_support: 101 }))).toThrow();
    expect(() => parseReviewTask(withScores({ ...SCORES, claim_support: 4.5 }))).toThrow();
    expect(() => parseReviewTask(withScores({ technical_accuracy: 50 }))).toThrow();
    expect(() => parseReviewTask(withScores({ ...SCORES, extra: 1 }))).toThrow();
  });

  it('allow only https reference links without credentials', () => {
    expect(safeReferenceUrl('https://example.org/a')).toBe('https://example.org/a');
    for (const bad of ['http://example.org', 'javascript:alert(1)', 'https://user:pw@example.org/', '//example.org', 'data:text/html,x', 42, null]) {
      expect(safeReferenceUrl(bad), String(bad)).toBeNull();
    }
  });

  it('parse the report list without scores', () => {
    const list = parseReportList({
      reports: [{ interview_id: SES, generated_at: '2026-11-04T13:13:52Z', plan: { plan_id: 'plan_demo_backend_en', version: 1, synthetic: true }, language: 'en', answer_count: 4 }],
    });
    expect(list[0]).toEqual({ interview_id: SES, generated_at: '2026-11-04T13:13:52Z', plan: { plan_id: 'plan_demo_backend_en', version: 1, synthetic: true }, language: 'en', answer_count: 4 });
  });

  it('parse a report and insist that it is decision support only', () => {
    const report = {
      report_version: 1,
      interview_id: SES,
      generated_at: '2026-11-04T13:13:52.42140755Z',
      plan: { plan_id: 'plan_demo_backend_en', version: 1, synthetic: true },
      language: 'en',
      decision_support_only: true,
      notice: 'Decision support only.',
      answers: [
        {
          answer_id: ANS,
          item_id: 'q1',
          question: 'How does MVCC work?',
          rubric: 'rub_demo_postgres_mvcc@1',
          rubric_synthetic: true,
          reviewed: { scores: SCORES, weighted_score: 50, rationale: 'Fine answer.', reviewer_id: 'stf_ABCDEFGHIJKLMNOPQRSTUV', decided_at: '2026-11-04T13:13:52Z' },
          review_reasons: ['policy_all_reviewed'],
          ai_evaluation: null,
        },
      ],
    };
    const parsed = parseReport(report);
    expect(parsed.answers[0]?.reviewed.ai_draft_viewed).toBeNull();
    expect(() => parseReport({ ...report, decision_support_only: false })).toThrow();
    expect(() => parseReport({ ...report, report_version: 2 })).toThrow();

    // The AI part of a report is parsed like the task's draft, without the advisory step and the output risk.
    const { advisory_next_step: _step, evaluator_output_risk: _risk, ...aiPart } = task.ai_evaluation;
    const first = report.answers[0];
    const withAI = { ...report, answers: [{ ...first, item_id: '2', reviewed: { ...first?.reviewed, ai_draft_viewed: true }, ai_evaluation: aiPart }] };
    const answer = parseReport(withAI).answers[0];
    expect(answer?.item_id).toBe('2');
    expect(answer?.reviewed.ai_draft_viewed).toBe(true);
    expect(answer?.ai_evaluation?.evidence[0]?.quote).toBe('a new row version');
    expect(answer?.ai_evaluation).not.toHaveProperty('advisory_next_step');
  });
});
