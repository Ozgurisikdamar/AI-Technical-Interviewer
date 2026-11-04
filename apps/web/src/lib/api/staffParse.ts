/**
 * Runtime validation of the review console's API responses. A response that breaks the contract
 * is rejected (the client turns that into an `invalid_response` ApiError) instead of reaching
 * the reviewer. What may grow later degrades safely: unknown reason codes are kept (and shown
 * only with the AI draft), an unknown interview state becomes 'UNKNOWN', and a reference URL that
 * is not https is dropped, not linked.
 */
import { isInterviewId, isReviewTaskId } from './ids';
import { state } from './parse';
import { ResponseShapeError, array, boolean, integer, nonEmptyString, record, string, type Json } from './shape';
import {
  ASSESSMENTS,
  DIMENSIONS,
  PAGE_LIMIT,
  STAFF_ROLES,
  type AIDraft,
  type Assessment,
  type Dimension,
  type DimensionScores,
  type EvidenceItem,
  type PlanRef,
  type RecordedDecision,
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

/**
 * Upper bounds on what the control plane sends. Each is at least the server's own limit, so no
 * valid response is refused; they keep a broken one from reaching the page. Lengths are counted
 * as the server counts them (shape.string).
 */
const MAX = {
  /** The plan contract allows 30 items with up to 5 follow-ups each, one answer apiece. */
  answers: 30 * (1 + 5),
  /** About 1,500 of the smallest evidence items fit in an evaluator output (MaxEvaluatorOutput, 256 KiB). */
  evidence: 2_000,
  /** A quote or a claim is never longer than the evaluator output that holds it. */
  evidenceText: 256 * 1024,
  /** Tier-1 ingestion refuses a segment text over 20,000 bytes (interview/ingest.go); typed answers are shorter. */
  segmentText: 20_000,
  /** The server sets no limit on segments per answer: this is far above a real answer. Evidence cites a subset. */
  segments: 10_000,
  /** The plan contract's question text. */
  question: 4_000,
  /** SubmitReviewOutcome and the review_outcomes check. */
  rationale: 4_000,
  /** The staff_tokens label check. */
  label: 100,
} as const;

const ID = {
  answer: /^ans_[A-Za-z0-9]{16,64}$/,
  segment: /^seg_[A-Za-z0-9]{16,64}$/,
  evaluation: /^evl_[A-Za-z0-9]{16,64}$/,
  outcome: /^rvo_[A-Za-z0-9]{16,64}$/,
  staff: /^stf_[A-Za-z0-9]{16,64}$/,
  /** The plan contract's item_id. */
  item: /^[a-z0-9_]{1,32}$/,
  rubric: /^rub_[a-z0-9_]{3,60}$/,
  rubricRef: /^rub_[a-z0-9_]{3,60}@[1-9][0-9]{0,8}$/,
  criterion: /^c_[a-z0-9_]{2,40}$/,
  reference: /^ref_[a-z0-9_]{2,60}$/,
  plan: /^plan_[a-z0-9_]{3,60}$/,
} as const;

const REASON = /^[a-z0-9_]{1,64}$/;
const LANGUAGE = /^[a-z]{2}$/;
/** RFC 3339 timestamps as the control plane writes them (UTC or with an offset). */
const TIMESTAMP = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$/;

/** A string that `valid` accepts. */
function checked(obj: Json, key: string, field: string, valid: (value: string) => boolean, maxLength = 128): string {
  const value = string(obj, key, field, maxLength);
  if (!valid(value)) throw new ResponseShapeError(`${field}.${key}`);
  return value;
}

function matching(obj: Json, key: string, field: string, pattern: RegExp, maxLength = 128): string {
  return checked(obj, key, field, (value) => pattern.test(value), maxLength);
}

function timestamp(obj: Json, key: string, field: string): string {
  return checked(obj, key, field, (value) => TIMESTAMP.test(value) && !Number.isNaN(Date.parse(value)), 40);
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

/** Reason codes, each once, in the order given. */
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

function oneOf<T extends string>(values: readonly T[], value: unknown, field: string): T {
  if (typeof value !== 'string' || !(values as readonly string[]).includes(value)) throw new ResponseShapeError(field);
  return value as T;
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

export function parseStaffSession(body: unknown): StaffSession {
  const obj = record(body, 'session');
  return {
    role: oneOf<StaffRole>(STAFF_ROLES, obj.role, 'session.role'),
    label: string(obj, 'label', 'session', MAX.label),
    expires_at: timestamp(obj, 'expires_at', 'session'),
  };
}

export function parseSessionEnded(body: unknown): { ended: boolean } {
  return { ended: boolean(record(body, 'session'), 'ended', 'session') };
}

function taskSummary(value: unknown, field: string): ReviewTaskSummary {
  const obj = record(value, field);
  return {
    task_id: checked(obj, 'task_id', field, isReviewTaskId),
    interview_id: checked(obj, 'interview_id', field, isInterviewId),
    answer_id: matching(obj, 'answer_id', field, ID.answer),
    item_id: matching(obj, 'item_id', field, ID.item),
    status: oneOf<ReviewStatus>(['open', 'completed'], obj.status, `${field}.status`),
    reasons: reasons(obj, 'reasons', field),
    has_ai_evaluation: boolean(obj, 'has_ai_evaluation', field),
    created_at: timestamp(obj, 'created_at', field),
    completed_at: nullableTimestamp(obj, 'completed_at', field),
  };
}

export function parseReviewTaskList(body: unknown): ReviewTaskSummary[] {
  const obj = record(body, 'queue');
  return array(obj, 'review_tasks', 'queue', PAGE_LIMIT).map((item, i) => taskSummary(item, `queue.review_tasks[${i}]`));
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
  const weightsObj = record(obj.weights, 'rubric.weights');
  const weights = {} as Record<Dimension, number>;
  for (const d of DIMENSIONS) weights[d] = number(weightsObj, d, 'rubric.weights', Number.MIN_VALUE, 1);
  const criteria: RubricCriterion[] = array(obj, 'criteria', 'rubric', 50).map((item, i) => {
    const field = `rubric.criteria[${i}]`;
    const c = record(item, field);
    return {
      criterion_id: matching(c, 'criterion_id', field, ID.criterion),
      dimension: oneOf<Dimension>(DIMENSIONS, c.dimension, `${field}.dimension`),
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
    status: oneOf<Rubric['status']>(['draft', 'approved'], obj.status, 'rubric.status'),
    synthetic: boolean(obj, 'synthetic', 'rubric'),
    title: nonEmptyString(obj, 'title', 'rubric', 200),
    weights,
    criteria,
    references,
  };
}

function nullableRubricRef(obj: Json, key: string, field: string): string | null {
  return obj[key] === null ? null : matching(obj, key, field, ID.rubricRef);
}

function transcript(obj: Json): TranscriptExcerpt[] {
  return array(obj, 'transcript', 'task', MAX.segments).map((item, i) => {
    const field = `task.transcript[${i}]`;
    const s = record(item, field);
    return {
      segment_id: matching(s, 'segment_id', field, ID.segment),
      start_ms: integer(s, 'start_ms', field, 0),
      end_ms: integer(s, 'end_ms', field, 0),
      text: string(s, 'text', field, MAX.segmentText),
    };
  });
}

function evidence(obj: Json, field: string): EvidenceItem[] {
  return array(obj, 'evidence', field, MAX.evidence).map((item, i) => {
    const f = `${field}.evidence[${i}]`;
    const e = record(item, f);
    return {
      dimension: oneOf<Dimension>(DIMENSIONS, e.dimension, `${f}.dimension`),
      transcript_start_ms: integer(e, 'transcript_start_ms', f, 0),
      transcript_end_ms: integer(e, 'transcript_end_ms', f, 0),
      transcript_segment_ids: stringList(e, 'transcript_segment_ids', f, ID.segment, MAX.segments),
      quote: string(e, 'quote', f, MAX.evidenceText),
      claim: string(e, 'claim', f, MAX.evidenceText),
      assessment: oneOf<Assessment>(ASSESSMENTS, e.assessment, `${f}.assessment`),
      reference_ids: stringList(e, 'reference_ids', f, ID.reference),
    };
  });
}

/** What the task's AI draft and the report's AI part share. */
function aiCore(obj: Json, field: string): ReportAIPart {
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

function aiDraft(value: unknown): AIDraft | null {
  if (value === null) return null;
  const obj = record(value, 'ai_evaluation');
  return {
    ...aiCore(obj, 'ai_evaluation'),
    advisory_next_step: matching(obj, 'advisory_next_step', 'ai_evaluation', /^[A-Z_]{1,40}$/),
    evaluator_output_risk: matching(obj, 'evaluator_output_risk', 'ai_evaluation', /^[a-z_]{1,40}$/),
  };
}

/** What the task's outcome and the report's `reviewed` block share. */
function decisionCore(obj: Json, field: string): RecordedDecision {
  return {
    scores: nullableScores(obj.scores, `${field}.scores`),
    weighted_score: nullableNumber(obj, 'weighted_score', field, 0, 100),
    rationale: string(obj, 'rationale', field, MAX.rationale),
    reviewer_id: matching(obj, 'reviewer_id', field, ID.staff),
    decided_at: timestamp(obj, 'decided_at', field),
  };
}

function outcome(value: unknown): ReviewOutcome | null {
  if (value === null) return null;
  const obj = record(value, 'outcome');
  return {
    ...decisionCore(obj, 'outcome'),
    outcome_id: matching(obj, 'outcome_id', 'outcome', ID.outcome),
    ai_draft_viewed: boolean(obj, 'ai_draft_viewed', 'outcome'),
  };
}

export function parseReviewTask(body: unknown): ReviewTask {
  const obj = record(body, 'task');
  return {
    ...taskSummary(obj, 'task'),
    language: language(obj, 'language', 'task'),
    question: nonEmptyString(obj, 'question', 'task', MAX.question),
    rubric_ref: nullableRubricRef(obj, 'rubric_ref', 'task'),
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
    interview_state: state(obj.interview_state, 'result.interview_state'),
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
  return array(obj, 'reports', 'reports', PAGE_LIMIT).map((item, i) => {
    const field = `reports.reports[${i}]`;
    const r = record(item, field);
    return {
      interview_id: checked(r, 'interview_id', field, isInterviewId),
      generated_at: timestamp(r, 'generated_at', field),
      plan: plan(r.plan, `${field}.plan`),
      language: language(r, 'language', field),
      answer_count: integer(r, 'answer_count', field, 0),
    };
  });
}

function reportAnswer(value: unknown, i: number): ReportAnswer {
  const field = `report.answers[${i}]`;
  const obj = record(value, field);
  const reviewed = record(obj.reviewed, `${field}.reviewed`);
  // Reports written before the console recorded it have no ai_draft_viewed.
  const viewed = reviewed.ai_draft_viewed;
  if (viewed !== undefined && typeof viewed !== 'boolean') throw new ResponseShapeError(`${field}.reviewed.ai_draft_viewed`);
  const synthetic = obj.rubric_synthetic;
  if (synthetic !== null && typeof synthetic !== 'boolean') throw new ResponseShapeError(`${field}.rubric_synthetic`);
  const ai = obj.ai_evaluation ?? null;
  return {
    answer_id: matching(obj, 'answer_id', field, ID.answer),
    item_id: matching(obj, 'item_id', field, ID.item),
    question: nonEmptyString(obj, 'question', field, MAX.question),
    rubric: nullableRubricRef(obj, 'rubric', field),
    rubric_synthetic: synthetic,
    reviewed: { ...decisionCore(reviewed, `${field}.reviewed`), ai_draft_viewed: viewed ?? null },
    review_reasons: reasons(obj, 'review_reasons', field),
    ai_evaluation: ai === null ? null : aiCore(record(ai, `${field}.ai_evaluation`), `${field}.ai_evaluation`),
  };
}

export function parseReport(body: unknown): Report {
  const obj = record(body, 'report');
  if (obj.report_version !== 1) throw new ResponseShapeError('report.report_version');
  if (obj.decision_support_only !== true) throw new ResponseShapeError('report.decision_support_only');
  return {
    interview_id: checked(obj, 'interview_id', 'report', isInterviewId),
    generated_at: timestamp(obj, 'generated_at', 'report'),
    plan: plan(obj.plan, 'report.plan'),
    language: language(obj, 'language', 'report'),
    notice: nonEmptyString(obj, 'notice', 'report', 1_000),
    answers: array(obj, 'answers', 'report', MAX.answers).map(reportAnswer),
  };
}
