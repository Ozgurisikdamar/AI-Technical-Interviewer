/**
 * Why an answer is in the queue. Every answer is reviewed (`policy_all_reviewed`); the other
 * reasons say what to look at. Reasons about the process are shown with the task. Reasons that
 * come from the AI evaluation would anchor the reviewer on the AI's judgement, so they are shown
 * only with the AI draft (ADR-023, independent first), as are reasons this console does not know.
 */

/** Reasons that describe the process or the recording, not the AI's view of the answer. */
export const PROCESS_REASONS: ReadonlySet<string> = new Set([
  'policy_all_reviewed',
  'pipeline_failed',
  'no_rubric',
  'synthetic_source',
  'not_evaluated',
  'evaluation_failed',
  'evaluator_output_invalid',
  'evaluator_abstained',
  'missing_segments',
  'integrity_flags',
]);

export interface SplitReasons {
  readonly process: readonly string[];
  /** AI-derived and unknown reasons: shown only with the AI draft. */
  readonly withDraft: readonly string[];
}

/** Splits reasons (each once, as the parser leaves them) into the two groups, keeping their order. */
export function splitReasons(reasons: readonly string[]): SplitReasons {
  return { process: reasons.filter((r) => PROCESS_REASONS.has(r)), withDraft: reasons.filter((r) => !PROCESS_REASONS.has(r)) };
}
