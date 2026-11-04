/**
 * Checks a reviewer's decision before it is sent, with the server's rules (SubmitReviewOutcome):
 * an integer from 0 to 100 for every dimension of the rubric, and a rationale of 10 to 4000
 * characters. An answer whose question has no rubric is reviewed with a rationale only; a rubric
 * that should exist but is missing blocks the decision (the server would refuse it).
 */
import { DIMENSIONS, type Dimension, type DimensionScores, type ReviewDecision, type ReviewTask } from '@/lib/api/staffTypes';

export const RATIONALE_MIN = 10;
export const RATIONALE_MAX = 4_000;
const SCORE = /^\d{1,3}$/;

export type ScoreInputs = Readonly<Partial<Record<Dimension, string>>>;
export type ScoreProblem = 'required' | 'invalid';
export type RationaleProblem = 'required' | 'too_short' | 'too_long';

/** How the task can be decided: with scores, with a rationale only, or not here at all. */
export type DecisionMode = 'scored' | 'rationale_only' | 'blocked';

/** A rubric means scores; no rubric_ref means the question has none; a rubric_ref without a rubric is not stored. */
export function decisionMode(task: Pick<ReviewTask, 'rubric' | 'rubric_ref'>): DecisionMode {
  if (task.rubric !== null) return 'scored';
  return task.rubric_ref === null ? 'rationale_only' : 'blocked';
}

/** The score a reviewer typed, or the problem with it. */
export function parseScore(input: string | undefined): number | ScoreProblem {
  const text = (input ?? '').trim();
  if (text === '') return 'required';
  if (!SCORE.test(text)) return 'invalid';
  const value = Number(text);
  return value <= 100 ? value : 'invalid';
}

/** Characters as the server counts them (code points), after trimming. */
export function rationaleLength(rationale: string): number {
  return [...rationale.trim()].length;
}

export function rationaleProblem(rationale: string): RationaleProblem | null {
  const n = rationaleLength(rationale);
  if (n === 0) return 'required';
  if (n < RATIONALE_MIN) return 'too_short';
  if (n > RATIONALE_MAX) return 'too_long';
  return null;
}

export interface DecisionProblems {
  readonly scores: Readonly<Partial<Record<Dimension, ScoreProblem>>>;
  readonly rationale: RationaleProblem | null;
}

/** The fields with a problem, in the order the form shows them (for the error summary). */
export type ProblemField = Dimension | 'rationale';

export type DecisionCheck = {
  /** The scores typed so far that are valid, for the weighted preview. */
  readonly values: Readonly<Partial<Record<Dimension, number>>>;
} & (
  | { readonly ok: true; readonly decision: ReviewDecision }
  | { readonly ok: false; readonly problems: DecisionProblems; readonly fields: readonly ProblemField[] }
);

export function checkDecision(
  mode: Exclude<DecisionMode, 'blocked'>,
  inputs: ScoreInputs,
  rationale: string,
  aiDraftViewed: boolean,
): DecisionCheck {
  const scores: Partial<Record<Dimension, ScoreProblem>> = {};
  const values: Partial<Record<Dimension, number>> = {};
  const fields: ProblemField[] = [];
  if (mode === 'scored') {
    for (const d of DIMENSIONS) {
      const parsed = parseScore(inputs[d]);
      if (typeof parsed === 'number') {
        values[d] = parsed;
      } else {
        scores[d] = parsed;
        fields.push(d);
      }
    }
  }
  const problem = rationaleProblem(rationale);
  if (problem !== null) fields.push('rationale');
  if (fields.length > 0) return { values, ok: false, problems: { scores, rationale: problem }, fields };
  return {
    values,
    ok: true,
    // Every dimension has a valid score here, so `values` is complete.
    decision: { scores: mode === 'scored' ? (values as DimensionScores) : null, rationale: rationale.trim(), ai_draft_viewed: aiDraftViewed },
  };
}
