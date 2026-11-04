/**
 * Runtime validation of the review console's API responses. A response that breaks the contract
 * is rejected (the client turns that into an `invalid_response` ApiError) instead of reaching
 * the reviewer. What may grow later degrades safely: unknown reason codes are kept (and shown
 * only with the AI draft), and a reference URL that is not https is dropped, not linked.
 */
import { isInterviewId } from './ids';
import { ResponseShapeError, array, boolean, integer, nonEmptyString, record, string, type Json } from './shape';
import {
  ASSESSMENTS,
  DIMENSIONS,
  STAFF_ROLES,
  type AIDraft,
  type Assessment,
  type Dimension,
  type DimensionScores,
  type EvidenceItem,
  type PlanRef,
  type Report,
  type ReportAIPart,
  type ReportAnswer,
  type ReportSummary,
  type ReviewOutcome,
  type ReviewOutcomeResult,
  type ReviewStatus,
  type ReviewTask,
  type ReviewTaskSummary,
  type Rubric,
  type RubricCriterion,
  type RubricReference,
  type StaffRole,
  type StaffSession,
  type TranscriptExcerpt,
} from './staffTypes';

const ID = {
  task: /^rvt_[A-Za-z0-9]{16,64}$/,
  answer: /^ans_[A-Za-z0-9]{16,64}$/,
  segment: /^seg_[A-Za-z0-9]{16,64}$/,
  evaluation: /^evl_[A-Za-z0-9]{16,64}$/,
  outcome: /^rvo_[A-Za-z0-9]{16,64}$/,
  staff: /^stf_[A-Za-z0-9]{16,64}$/,
  item: /^[a-z][a-z0-9_]{0,63}$/,
  rubric: /^rub_[a-z0-9_]{3,60}$/,
  criterion: /^c_[a-z0-9_]{2,40}$/,
  reference: /^ref_[a-z0-9_]{2,60}$/,
  plan: /^plan_[a-z0-9_]{1,80}$/,
} as const;

const REASON = /^[a-z0-9_]{1,64}$/;
const LANGUAGE = /^[a-z]{2}$/;
/** RFC 3339 timestamps as the control plane writes them (UTC or with an offset). */
const TIMESTAMP = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$/;

function matching(obj: Json, key: string, field: string, pattern: RegExp, maxLength = 128): string {
  const value = string(obj, key, field, maxLength);
  if (!pattern.test(value)) throw new ResponseShapeError(`${field}.${key}`);
  return value;
}

function interviewId(obj: Json, key: string, field: string): string {
  const value = string(obj, key, field, 128);
  if (!isInterviewId(value)) throw new ResponseShapeError(`${field}.${key}`);
  return value;
}

function timestamp(obj: Json, key: string, field: string): string {
  const value = matching(obj, key, field, TIMESTAMP, 40);
  if (Number.isNaN(Date.parse(value))) throw new ResponseShapeError(`${field}.${key}`);
  return value;
}

function nullableTimestamp(obj: Json, key: string, field: string): string | null {
  return obj[key] === null ? null : timestamp(obj, key, field);
}

function number(obj: Json, key: string, field: string, min: number, max: number): number {
  const value = obj[key];
  if (typeof value !== 'number' || !Number.isFinite(value) || value < min || value > max) {
    throw new ResponseShapeError(`${field}.${key}`);
  }
  return value;
}

function nullableNumber(obj: Json, key: string, field: string, min: number, max: number): number | null {
  return obj[key] === null ? null : number(obj, key, field, min, max);
}

function reasons(obj: Json, key: string, field: string): string[] {
  const out: string[] = [];
  for (const [i, item] of array(obj, key, field, 64).entries()) {
    if (typeof item !== 'string' || !REASON.test(item)) throw new ResponseShapeError(`${field}.${key}[${i}]`);
    if (!out.includes(item)) out.push(item);
  }
  return out;
}

function stringList(obj: Json, key: string, field: string, pattern: RegExp, maxItems = 100): string[] {
  return array(obj, key, field, maxItems).map((item, i) => {
    if (typeof item !== 'string' || !pattern.test(item)) throw new ResponseShapeError(`${field}.${key}[${i}]`);
    return item;
  });
}

function dimension(value: unknown, field: string): Dimension {
  if (typeof value !== 'string' || !(DIMENSIONS as readonly string[]).includes(value)) throw new ResponseShapeError(field);
  return value as Dimension;
}

/** Exactly the four dimensions, each an integer from 0 to 100. */
function scores(value: unknown, field: string): DimensionScores {
  const obj = record(value, field);
  if (Object.keys(obj).length !== DIMENSIONS.length) throw new ResponseShapeError(field);
  const out = {} as Record<Dimension, number>;
  for (const d of DIMENSIONS) {
    const v = obj[d];
    if (typeof v !== 'number' || !Number.isInteger(v) || v < 0 || v > 100) throw new ResponseShapeError(`${field}.${d}`);
    out[d] = v;
  }
  return out;
}

function nullableScores(value: unknown, field: string): DimensionScores | null {
  return value === null ? null : scores(value, field);
}

function language(obj: Json, key: string, field: string): string {
  return matching(obj, key, field, LANGUAGE, 2);
}

function role(value: unknown): StaffRole {
  if (typeof value !== 'string' || !(STAFF_ROLES as readonly string[]).includes(value)) throw new ResponseShapeError('session.role');
  return value as StaffRole;
}

export function parseStaffSession(body: unknown): StaffSession {
  const obj = record(body, 'session');
  return { role: role(obj.role), label: string(obj, 'label', 'session', 100), expires_at: timestamp(obj, 'expires_at', 'session') };
}

export function parseSessionEnded(body: unknown): { ended: boolean } {
  return { ended: boolean(record(body, 'session'), 'ended', 'session') };
}

function taskSummary(value: unknown, field: string): ReviewTaskSummary {
  const obj = record(value, field);
  const status = obj.status;
  if (status !== 'open' && status !== 'completed') throw new ResponseShapeError(`${field}.status`);
  return {
    task_id: matching(obj, 'task_id', field, ID.task),
    interview_id: interviewId(obj, 'interview_id', field),
    answer_id: matching(obj, 'answer_id', field, ID.answer),
    item_id: matching(obj, 'item_id', field, ID.item),
    status: status as ReviewStatus,
    reasons: reasons(obj, 'reasons', field),
    has_ai_evaluation: boolean(obj, 'has_ai_evaluation', field),
    created_at: timestamp(obj, 'created_at', field),
    completed_at: nullableTimestamp(obj, 'completed_at', field),
  };
}

export function parseReviewTaskList(body: unknown): ReviewTaskSummary[] {
  const obj = record(body, 'queue');
  return array(obj, 'review_tasks', 'queue').map((item, i) => taskSummary(item, `queue.review_tasks[${i}]`));
}

/** An https URL without credentials, or null: anything else is never rendered as a link. */
export function safeReferenceUrl(value: unknown): string | null {
  if (typeof value !== 'string' || value.length > 2_000) return null;
  try {
    const url = new URL(value);
    return url.protocol === 'https:' && url.username === '' && url.password === '' ? url.href : null;
  } catch {
    return null;
  }
}

function rubric(value: unknown): Rubric | null {
  if (value === null) return null;
  const obj = record(value, 'rubric');
  const status = obj.status;
  if (status !== 'draft' && status !== 'approved') throw new ResponseShapeError('rubric.status');
  const weightsObj = record(obj.weights, 'rubric.weights');
  const weights = {} as Record<Dimension, number>;
  for (const d of DIMENSIONS) weights[d] = number(weightsObj, d, 'rubric.weights', Number.MIN_VALUE, 1);
  const criteria: RubricCriterion[] = array(obj, 'criteria', 'rubric', 50).map((item, i) => {
    const field = `rubric.criteria[${i}]`;
    const c = record(item, field);
    return {
      criterion_id: matching(c, 'criterion_id', field, ID.criterion),
      dimension: dimension(c.dimension, `${field}.dimension`),
      description: nonEmptyString(c, 'description', field, 500),
      reference_ids: stringList(c, 'reference_ids', field, ID.reference),
    };
  });
  const references: RubricReference[] = array(obj, 'references', 'rubric', 50).map((item, i) => {
    const field = `rubric.references[${i}]`;
    const r = record(item, field);
    return {
      reference_id: matching(r, 'reference_id', field, ID.reference),
      title: nonEmptyString(r, 'title', field, 200),
      url: safeReferenceUrl(r.url),
      summary: nonEmptyString(r, 'summary', field, 1_000),
    };
  });
  return {
    rubric_id: matching(obj, 'rubric_id', 'rubric', ID.rubric),
    version: integer(obj, 'version', 'rubric', 1),
    status,
    synthetic: boolean(obj, 'synthetic', 'rubric'),
    title: nonEmptyString(obj, 'title', 'rubric', 200),
    weights,
    criteria,
    references,
  };
}

function transcript(obj: Json): TranscriptExcerpt[] {
  return array(obj, 'transcript', 'task', 500).map((item, i) => {
    const field = `task.transcript[${i}]`;
    const s = record(item, field);
    return {
      segment_id: matching(s, 'segment_id', field, ID.segment),
      start_ms: integer(s, 'start_ms', field, 0),
      end_ms: integer(s, 'end_ms', field, 0),
      text: string(s, 'text', field, 20_000),
    };
  });
}

function assessment(value: unknown, field: string): Assessment {
  if (typeof value !== 'string' || !(ASSESSMENTS as readonly string[]).includes(value)) throw new ResponseShapeError(field);
  return value as Assessment;
}

function evidence(obj: Json, field: string): EvidenceItem[] {
  return array(obj, 'evidence', field, 200).map((item, i) => {
    const f = `${field}.evidence[${i}]`;
    const e = record(item, f);
    return {
      dimension: dimension(e.dimension, `${f}.dimension`),
      transcript_start_ms: integer(e, 'transcript_start_ms', f, 0),
      transcript_end_ms: integer(e, 'transcript_end_ms', f, 0),
      transcript_segment_ids: stringList(e, 'transcript_segment_ids', f, ID.segment),
      quote: string(e, 'quote', f, 5_000),
      claim: string(e, 'claim', f, 5_000),
      assessment: assessment(e.assessment, `${f}.assessment`),
      reference_ids: stringList(e, 'reference_ids', f, ID.reference),
    };
  });
}

function aiDraft(value: unknown): AIDraft | null {
  if (value === null) return null;
  const obj = record(value, 'ai_evaluation');
  return {
    evaluation_id: matching(obj, 'evaluation_id', 'ai_evaluation', ID.evaluation),
    evaluator_version: nonEmptyString(obj, 'evaluator_version', 'ai_evaluation', 200),
    scores: scores(obj.scores, 'ai_evaluation.scores'),
    weighted_score: number(obj, 'weighted_score', 'ai_evaluation', 0, 100),
    confidence: number(obj, 'confidence', 'ai_evaluation', 0, 1),
    calibrated: boolean(obj, 'calibrated', 'ai_evaluation'),
    advisory_next_step: matching(obj, 'advisory_next_step', 'ai_evaluation', /^[A-Z_]{1,40}$/),
    evaluator_output_risk: matching(obj, 'evaluator_output_risk', 'ai_evaluation', /^[a-z_]{1,40}$/),
    routing_reasons: reasons(obj, 'routing_reasons', 'ai_evaluation'),
    evidence: evidence(obj, 'ai_evaluation'),
  };
}

function outcome(value: unknown): ReviewOutcome | null {
  if (value === null) return null;
  const obj = record(value, 'outcome');
  return {
    outcome_id: matching(obj, 'outcome_id', 'outcome', ID.outcome),
    reviewer_id: matching(obj, 'reviewer_id', 'outcome', ID.staff),
    scores: nullableScores(obj.scores, 'outcome.scores'),
    weighted_score: nullableNumber(obj, 'weighted_score', 'outcome', 0, 100),
    rationale: string(obj, 'rationale', 'outcome', 5_000),
    ai_draft_viewed: boolean(obj, 'ai_draft_viewed', 'outcome'),
    decided_at: timestamp(obj, 'decided_at', 'outcome'),
  };
}

export function parseReviewTask(body: unknown): ReviewTask {
  const obj = record(body, 'task');
  return {
    ...taskSummary(obj, 'task'),
    language: language(obj, 'language', 'task'),
    question: nonEmptyString(obj, 'question', 'task', 5_000),
    rubric: rubric(obj.rubric ?? null),
    transcript: transcript(obj),
    ai_evaluation: aiDraft(obj.ai_evaluation),
    outcome: outcome(obj.outcome),
  };
}

export function parseReviewOutcomeResult(body: unknown): ReviewOutcomeResult {
  const obj = record(body, 'result');
  return {
    outcome_id: matching(obj, 'outcome_id', 'result', ID.outcome),
    interview_state: matching(obj, 'interview_state', 'result', /^[A-Z][A-Z_]{0,39}$/),
    open_review_tasks: integer(obj, 'open_review_tasks', 'result', 0),
  };
}

function plan(value: unknown, field: string): PlanRef {
  const obj = record(value, field);
  return {
    plan_id: matching(obj, 'plan_id', field, ID.plan),
    version: integer(obj, 'version', field, 1),
    synthetic: boolean(obj, 'synthetic', field),
  };
}

export function parseReportList(body: unknown): ReportSummary[] {
  const obj = record(body, 'reports');
  return array(obj, 'reports', 'reports').map((item, i) => {
    const field = `reports.reports[${i}]`;
    const r = record(item, field);
    return {
      interview_id: interviewId(r, 'interview_id', field),
      generated_at: timestamp(r, 'generated_at', field),
      plan: plan(r.plan, `${field}.plan`),
      language: language(r, 'language', field),
      answer_count: integer(r, 'answer_count', field, 0),
    };
  });
}

function reportAI(value: unknown, field: string): ReportAIPart | null {
  if (value === null || value === undefined) return null;
  const obj = record(value, field);
  return {
    evaluation_id: matching(obj, 'evaluation_id', field, ID.evaluation),
    evaluator_version: nonEmptyString(obj, 'evaluator_version', field, 200),
    scores: scores(obj.scores, `${field}.scores`),
    weighted_score: number(obj, 'weighted_score', field, 0, 100),
    confidence: number(obj, 'confidence', field, 0, 1),
    calibrated: boolean(obj, 'calibrated', field),
    routing_reasons: reasons(obj, 'routing_reasons', field),
    evidence: evidence(obj, field),
  };
}

function reportAnswer(value: unknown, i: number): ReportAnswer {
  const field = `report.answers[${i}]`;
  const obj = record(value, field);
  const reviewed = record(obj.reviewed, `${field}.reviewed`);
  const viewed = reviewed.ai_draft_viewed;
  if (viewed !== undefined && typeof viewed !== 'boolean') throw new ResponseShapeError(`${field}.reviewed.ai_draft_viewed`);
  const synthetic = obj.rubric_synthetic;
  if (synthetic !== null && typeof synthetic !== 'boolean') throw new ResponseShapeError(`${field}.rubric_synthetic`);
  return {
    answer_id: matching(obj, 'answer_id', field, ID.answer),
    item_id: matching(obj, 'item_id', field, ID.item),
    question: nonEmptyString(obj, 'question', field, 5_000),
    rubric: obj.rubric === null ? null : matching(obj, 'rubric', field, /^rub_[a-z0-9_]{3,60}@[1-9][0-9]{0,5}$/),
    rubric_synthetic: synthetic,
    reviewed: {
      scores: nullableScores(reviewed.scores, `${field}.reviewed.scores`),
      weighted_score: nullableNumber(reviewed, 'weighted_score', `${field}.reviewed`, 0, 100),
      rationale: string(reviewed, 'rationale', `${field}.reviewed`, 5_000),
      ai_draft_viewed: viewed ?? null,
      reviewer_id: matching(reviewed, 'reviewer_id', `${field}.reviewed`, ID.staff),
      decided_at: timestamp(reviewed, 'decided_at', `${field}.reviewed`),
    },
    review_reasons: reasons(obj, 'review_reasons', field),
    ai_evaluation: reportAI(obj.ai_evaluation, `${field}.ai_evaluation`),
  };
}

export function parseReport(body: unknown): Report {
  const obj = record(body, 'report');
  if (obj.report_version !== 1) throw new ResponseShapeError('report.report_version');
  if (obj.decision_support_only !== true) throw new ResponseShapeError('report.decision_support_only');
  return {
    interview_id: interviewId(obj, 'interview_id', 'report'),
    generated_at: timestamp(obj, 'generated_at', 'report'),
    plan: plan(obj.plan, 'report.plan'),
    language: language(obj, 'language', 'report'),
    notice: nonEmptyString(obj, 'notice', 'report', 1_000),
    answers: array(obj, 'answers', 'report', 100).map(reportAnswer),
  };
}
