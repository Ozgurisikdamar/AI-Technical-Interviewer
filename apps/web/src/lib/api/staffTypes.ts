/**
 * Review console types: the staff operations of the public API (docs/contracts/openapi,
 * tags `review` and `staff`). Nothing here reaches a candidate's page. Scores are rubric scores
 * that a human reviewer records; AI evaluations are uncalibrated drafts (ADR-022, ADR-023).
 */

export const STAFF_ROLES = ['admin', 'recruiter', 'reviewer'] as const;
export type StaffRole = (typeof STAFF_ROLES)[number];

/** Who is signed in (GET /api/v1/staff/session). */
export interface StaffSession {
  readonly role: StaffRole;
  readonly label: string;
  /** ISO timestamp: when the credential used stops working. */
  readonly expires_at: string;
}

export const DIMENSIONS = ['technical_accuracy', 'architecture_maturity', 'edge_case_awareness', 'claim_support'] as const;
export type Dimension = (typeof DIMENSIONS)[number];
export type DimensionScores = Readonly<Record<Dimension, number>>;

export type ReviewStatus = 'open' | 'completed';

export interface ReviewTaskSummary {
  readonly task_id: string;
  readonly interview_id: string;
  readonly answer_id: string;
  readonly item_id: string;
  readonly status: ReviewStatus;
  /** Machine reasons; unknown ones are kept and shown only with the AI draft (lib/staff/reasons). */
  readonly reasons: readonly string[];
  readonly has_ai_evaluation: boolean;
  readonly created_at: string;
  readonly completed_at: string | null;
}

export interface RubricCriterion {
  readonly criterion_id: string;
  readonly dimension: Dimension;
  readonly description: string;
  readonly reference_ids: readonly string[];
}

export interface RubricReference {
  readonly reference_id: string;
  readonly title: string;
  /** https only; anything else is dropped by the parser and the title is shown without a link. */
  readonly url: string | null;
  readonly summary: string;
}

/** Rubric contract v1 as a reviewer sees it (the synthetic indicators are removed by the server). */
export interface Rubric {
  readonly rubric_id: string;
  readonly version: number;
  readonly status: 'draft' | 'approved';
  readonly synthetic: boolean;
  readonly title: string;
  /** Fractions of 1 that sum to 1; the weighted score uses them as whole percentages. */
  readonly weights: DimensionScores;
  readonly criteria: readonly RubricCriterion[];
  readonly references: readonly RubricReference[];
}

export interface TranscriptExcerpt {
  readonly segment_id: string;
  readonly start_ms: number;
  readonly end_ms: number;
  readonly text: string;
}

export const ASSESSMENTS = ['correct', 'partially_correct', 'incorrect', 'unsupported', 'not_addressed'] as const;
export type Assessment = (typeof ASSESSMENTS)[number];

export interface EvidenceItem {
  readonly dimension: Dimension;
  readonly transcript_start_ms: number;
  readonly transcript_end_ms: number;
  readonly transcript_segment_ids: readonly string[];
  readonly quote: string;
  readonly claim: string;
  readonly assessment: Assessment;
  readonly reference_ids: readonly string[];
}

/** The first valid AI evaluation of the answer: an uncalibrated draft, never a decision. */
export interface AIDraft {
  readonly evaluation_id: string;
  readonly evaluator_version: string;
  readonly scores: DimensionScores;
  readonly weighted_score: number;
  readonly confidence: number;
  readonly calibrated: boolean;
  /** The evaluator's advisory interview step; never a hiring recommendation. */
  readonly advisory_next_step: string;
  readonly evaluator_output_risk: string;
  readonly routing_reasons: readonly string[];
  readonly evidence: readonly EvidenceItem[];
}

export interface ReviewOutcome {
  readonly outcome_id: string;
  readonly reviewer_id: string;
  /** Null for an answer whose question has no rubric (reviewed with a rationale only). */
  readonly scores: DimensionScores | null;
  readonly weighted_score: number | null;
  readonly rationale: string;
  readonly ai_draft_viewed: boolean;
  readonly decided_at: string;
}

export interface ReviewTask extends ReviewTaskSummary {
  readonly language: string;
  readonly question: string;
  readonly rubric: Rubric | null;
  readonly transcript: readonly TranscriptExcerpt[];
  readonly ai_evaluation: AIDraft | null;
  readonly outcome: ReviewOutcome | null;
}

export interface ReviewDecision {
  readonly scores: DimensionScores | null;
  readonly rationale: string;
  readonly ai_draft_viewed: boolean;
}

export interface ReviewOutcomeResult {
  readonly outcome_id: string;
  readonly interview_state: string;
  readonly open_review_tasks: number;
}

export interface PlanRef {
  readonly plan_id: string;
  readonly version: number;
  readonly synthetic: boolean;
}

/** One entry of the report list: no scores and no candidate identity, so it cannot rank anyone. */
export interface ReportSummary {
  readonly interview_id: string;
  readonly generated_at: string;
  readonly plan: PlanRef;
  readonly language: string;
  readonly answer_count: number;
}

export interface ReportAIPart {
  readonly evaluation_id: string;
  readonly evaluator_version: string;
  readonly scores: DimensionScores;
  readonly weighted_score: number;
  readonly confidence: number;
  readonly calibrated: boolean;
  readonly routing_reasons: readonly string[];
  readonly evidence: readonly EvidenceItem[];
}

export interface ReportAnswer {
  readonly answer_id: string;
  readonly item_id: string;
  readonly question: string;
  /** rubric_id@version, or null when the question has no rubric. */
  readonly rubric: string | null;
  readonly rubric_synthetic: boolean | null;
  readonly reviewed: {
    readonly scores: DimensionScores | null;
    readonly weighted_score: number | null;
    readonly rationale: string;
    /** Null in reports written before the console recorded it. */
    readonly ai_draft_viewed: boolean | null;
    readonly reviewer_id: string;
    readonly decided_at: string;
  };
  readonly review_reasons: readonly string[];
  readonly ai_evaluation: ReportAIPart | null;
}

/** The decision-support report (REPORT_READY). It holds no hiring decision or recommendation. */
export interface Report {
  readonly interview_id: string;
  readonly generated_at: string;
  readonly plan: PlanRef;
  readonly language: string;
  readonly notice: string;
  readonly answers: readonly ReportAnswer[];
}
