import { describe, expect, it } from 'vitest';
import type { ReviewTaskSummary, StaffRole } from '@/lib/api/staffTypes';
import { checkDecision, decisionMode, parseScore, rationaleLength, rationaleProblem, validScores } from './decision';
import { canReview, homeFor, queueHref, queueParams, reportHref, sectionsFor, taskHref } from './nav';
import { groupByInterview, nextOpenTask } from './queue';
import { PROCESS_REASONS, splitReasons } from './reasons';
import { formatDateTime, formatOffset } from './time';
import { cleanToken, isStaffTokenFormat } from './token';
import { formatCenti, weightPercent, weightedCenti } from './weighted';

const WEIGHTS = { technical_accuracy: 0.4, architecture_maturity: 0.25, edge_case_awareness: 0.2, claim_support: 0.15 };
const SES_A = 'ses_AAAAAAAAAAAAAAAAAAAAAA';
const SES_B = 'ses_BBBBBBBBBBBBBBBBBBBBBB';

const task = (id: string, interview: string, status: 'open' | 'completed' = 'open'): ReviewTaskSummary => ({
  task_id: id,
  interview_id: interview,
  answer_id: 'ans_AAAAAAAAAAAAAAAAAAAAAA',
  item_id: 'q1',
  status,
  reasons: ['policy_all_reviewed'],
  has_ai_evaluation: false,
  created_at: '2026-11-04T10:00:00Z',
  completed_at: null,
});

describe('weighted score', () => {
  it('matches the control plane (whole-percent weights, hundredths of a point)', () => {
    expect([0.4, 0.25, 0.2, 0.15].map(weightPercent)).toEqual([40, 25, 20, 15]);
    const centi = weightedCenti({ technical_accuracy: 60, architecture_maturity: 50, edge_case_awareness: 40, claim_support: 30 }, WEIGHTS);
    expect(centi).toBe(4900);
    expect(formatCenti(centi ?? 0)).toBe('49.00');
    expect(weightedCenti({ technical_accuracy: 100, architecture_maturity: 100, edge_case_awareness: 100, claim_support: 100 }, WEIGHTS)).toBe(10_000);
    expect(weightedCenti({ technical_accuracy: 33, architecture_maturity: 67, edge_case_awareness: 1, claim_support: 99 }, WEIGHTS)).toBe(4_500);
  });

  it('is undefined until every dimension has a valid score', () => {
    expect(weightedCenti({ technical_accuracy: 60 }, WEIGHTS)).toBeNull();
    expect(weightedCenti({ technical_accuracy: 60, architecture_maturity: 50, edge_case_awareness: 40, claim_support: 101 }, WEIGHTS)).toBeNull();
    expect(weightedCenti({ technical_accuracy: 6.5, architecture_maturity: 50, edge_case_awareness: 40, claim_support: 30 }, WEIGHTS)).toBeNull();
  });
});

describe('decision', () => {
  it('accepts integers from 0 to 100 only', () => {
    expect(['0', '7', '100', ' 42 '].map(parseScore)).toEqual([0, 7, 100, 42]);
    expect(['', '  '].map(parseScore)).toEqual(['required', 'required']);
    expect(['101', '-1', '4.5', '1e2', '0x10', '٣', '1000', 'abc'].map(parseScore)).toEqual(Array(8).fill('invalid'));
  });

  it('counts rationale characters as the server does', () => {
    expect(rationaleLength('  ten chars!  ')).toBe(10);
    expect(rationaleLength('😀'.repeat(10))).toBe(10);
    expect(rationaleProblem('')).toBe('required');
    expect(rationaleProblem('too short')).toBe('too_short');
    expect(rationaleProblem('x'.repeat(4_001))).toBe('too_long');
    expect(rationaleProblem('Accurate but brief.')).toBeNull();
  });

  it('decides with scores, with a rationale only, or not at all', () => {
    expect(decisionMode({ rubric: {} as never, reasons: [] })).toBe('scored');
    expect(decisionMode({ rubric: null, reasons: ['policy_all_reviewed', 'no_rubric'] })).toBe('rationale_only');
    expect(decisionMode({ rubric: null, reasons: ['policy_all_reviewed'] })).toBe('blocked');
  });

  it('lists every problem in form order', () => {
    const check = checkDecision('scored', { technical_accuracy: '60', edge_case_awareness: '200' }, 'short', false);
    expect(check.ok).toBe(false);
    if (check.ok) return;
    expect(check.fields).toEqual(['architecture_maturity', 'edge_case_awareness', 'claim_support', 'rationale']);
    expect(check.problems.scores).toEqual({ architecture_maturity: 'required', edge_case_awareness: 'invalid', claim_support: 'required' });
    expect(check.problems.rationale).toBe('too_short');
  });

  it('builds the request body', () => {
    const inputs = { technical_accuracy: '60', architecture_maturity: '50', edge_case_awareness: '40', claim_support: '30' };
    expect(checkDecision('scored', inputs, '  Accurate but brief.  ', true)).toEqual({
      ok: true,
      decision: {
        scores: { technical_accuracy: 60, architecture_maturity: 50, edge_case_awareness: 40, claim_support: 30 },
        rationale: 'Accurate but brief.',
        ai_draft_viewed: true,
      },
    });
    expect(checkDecision('rationale_only', inputs, 'No rubric; the answer is on topic.', false)).toEqual({
      ok: true,
      decision: { scores: null, rationale: 'No rubric; the answer is on topic.', ai_draft_viewed: false },
    });
    expect(validScores({ technical_accuracy: '60', claim_support: 'x' })).toEqual({ technical_accuracy: 60 });
  });
});

describe('reasons', () => {
  it('shows process reasons and keeps AI-derived and unknown ones for the draft', () => {
    expect(splitReasons(['policy_all_reviewed', 'low_confidence', 'no_rubric', 'brand_new_reason', 'low_confidence'])).toEqual({
      process: ['policy_all_reviewed', 'no_rubric'],
      withDraft: ['low_confidence', 'brand_new_reason'],
    });
    for (const aiDerived of ['low_confidence', 'evaluator_output_risk', 'unsupported_claims', 'contradictions', 'follow_up_suggested', 'evaluator_requested_review', 'evaluator_disagreement', 'transcript_quality']) {
      expect(PROCESS_REASONS.has(aiDerived), aiDerived).toBe(false);
    }
  });
});

describe('queue', () => {
  it('groups by interview in queue order and finds the next open answer', () => {
    const tasks = [task('rvt_1111111111111111', SES_A), task('rvt_2222222222222222', SES_B), task('rvt_3333333333333333', SES_A)];
    expect(groupByInterview(tasks).map((g) => [g.interviewId, g.tasks.map((t) => t.task_id)])).toEqual([
      [SES_A, ['rvt_1111111111111111', 'rvt_3333333333333333']],
      [SES_B, ['rvt_2222222222222222']],
    ]);
    const a = [task('rvt_1111111111111111', SES_A, 'completed'), task('rvt_3333333333333333', SES_A)];
    expect(nextOpenTask(a, 'rvt_9999999999999999')?.task_id).toBe('rvt_3333333333333333');
    expect(nextOpenTask(a, 'rvt_3333333333333333')).toBeNull();
  });
});

describe('navigation', () => {
  it('follows the roles', () => {
    const roles: StaffRole[] = ['reviewer', 'admin', 'recruiter'];
    expect(roles.map(canReview)).toEqual([true, true, false]);
    expect(roles.map(sectionsFor)).toEqual([['review', 'reports'], ['review', 'reports'], ['reports']]);
    expect(roles.map(homeFor)).toEqual(['/staff/review', '/staff/review', '/staff/reports']);
  });

  it('builds only well-formed addresses', () => {
    expect(queueHref()).toBe('/staff/review');
    expect(queueHref('completed', SES_A)).toBe(`/staff/review?status=completed&interview=${SES_A}`);
    expect(queueHref('open', 'ses_x"><script>')).toBe('/staff/review');
    expect(taskHref('rvt_1111111111111111')).toBe('/staff/review/rvt_1111111111111111');
    expect(reportHref(SES_A)).toBe(`/staff/reports/${SES_A}`);
    expect(() => taskHref('../reports')).toThrow();
    expect(() => reportHref('ses_short')).toThrow();
  });

  it('validates the queue search parameters', () => {
    expect(queueParams({})).toEqual({ status: 'open', interviewId: undefined });
    expect(queueParams({ status: 'completed', interview: SES_A })).toEqual({ status: 'completed', interviewId: SES_A });
    expect(queueParams({ status: ['completed'], interview: 'ses_bad' })).toEqual({ status: 'open', interviewId: undefined });
  });
});

describe('time', () => {
  it('formats in UTC and labels it', () => {
    expect(formatDateTime('2026-11-04T13:13:52.411403Z')).toBe('4 Nov 2026, 13:13 UTC');
    expect(formatDateTime('2026-11-04T23:30:00-02:00')).toBe('5 Nov 2026, 01:30 UTC');
    expect(formatDateTime('not a time')).toBe('—');
    expect([0, 999, 83_500, 600_000].map(formatOffset)).toEqual(['0:00', '0:00', '1:23', '10:00']);
  });
});

describe('staff token', () => {
  it('accepts only the ati-admin token shape', () => {
    const token = `ati_st_${'A'.repeat(43)}`;
    expect(isStaffTokenFormat(cleanToken(`  ${token}\n`))).toBe(true);
    for (const bad of ['', 'ati_st_short', `ati_cs_${'A'.repeat(43)}`, `${token} x`, `ati_st_${'A'.repeat(20)}\n${'A'.repeat(20)}`, 'stf_AAAAAAAAAAAAAAAAAAAAAA']) {
      expect(isStaffTokenFormat(cleanToken(bad)), bad).toBe(false);
    }
  });
});
