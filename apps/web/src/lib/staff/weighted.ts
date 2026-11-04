/**
 * The weighted score, computed as the control plane does (evaluation.WeightedCenti): each
 * dimension's integer score times its weight in whole percent, summed in hundredths of a point.
 * The console shows it only as a preview; the server recomputes and stores its own.
 */
import { DIMENSIONS, type Dimension, type DimensionScores } from '@/lib/api/staffTypes';

/** A rubric weight (a fraction of 1) as whole percent. */
export function weightPercent(weight: number): number {
  return Math.round(weight * 100);
}

/** Hundredths of a point, or null while any dimension lacks a valid score. */
export function weightedCenti(scores: Partial<Record<Dimension, number>>, weights: DimensionScores): number | null {
  let total = 0;
  for (const d of DIMENSIONS) {
    const v = scores[d];
    if (v === undefined || !Number.isInteger(v) || v < 0 || v > 100) return null;
    total += v * weightPercent(weights[d]);
  }
  return total;
}

/** 4900 → "49.00". */
export function formatCenti(centi: number): string {
  return (centi / 100).toFixed(2);
}

/** A score in points as the API reports it (two decimals at most) → "49.00". */
export function formatPoints(points: number): string {
  return points.toFixed(2);
}
