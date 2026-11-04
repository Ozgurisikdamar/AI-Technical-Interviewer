"""Agreement and summary metrics for the offline benchmark (DATA_AND_ML_LIFECYCLE section 5)."""

from __future__ import annotations

import math
from collections.abc import Sequence

BANDS = 5  # scores 0-100 in bands of 20 points: 0-19, 20-39, 40-59, 60-79, 80-100


def band(score: int) -> int:
    if not 0 <= score <= 100:
        raise ValueError("scores are between 0 and 100")
    return min(score // 20, BANDS - 1)


def quadratic_weighted_kappa(a: Sequence[int], b: Sequence[int], categories: int = BANDS) -> float:
    """Cohen's kappa with quadratic weights for two raters over categories 0..categories-1."""
    if len(a) != len(b) or not a:
        raise ValueError("two non-empty rating lists of equal length are needed")
    n = len(a)
    observed = [[0.0] * categories for _ in range(categories)]
    for x, y in zip(a, b, strict=True):
        observed[x][y] += 1
    hist_a = [sum(row) for row in observed]
    hist_b = [sum(observed[i][j] for i in range(categories)) for j in range(categories)]
    num = den = 0.0
    for i in range(categories):
        for j in range(categories):
            w = (i - j) ** 2 / (categories - 1) ** 2
            num += w * observed[i][j]
            den += w * hist_a[i] * hist_b[j] / n
    if den == 0:
        return 1.0  # both raters used one and the same category throughout
    return 1 - num / den


def krippendorff_alpha_interval(units: Sequence[Sequence[float | None]]) -> float:
    """Krippendorff's alpha with the interval metric; None marks a missing rating."""
    values = [[v for v in u if v is not None] for u in units]
    pairable = [u for u in values if len(u) >= 2]
    n = sum(len(u) for u in pairable)
    if n < 2:
        raise ValueError("alpha needs at least one unit with two ratings")
    d_o = sum(sum((x - y) ** 2 for x in u for y in u) / (len(u) - 1) for u in pairable) / n
    flat = [v for u in pairable for v in u]
    total, squares = sum(flat), sum(v * v for v in flat)
    d_e = (2 * n * squares - 2 * total * total) / (n * (n - 1))
    if d_e == 0:
        return 1.0
    return 1 - d_o / d_e


def mean_absolute_error(a: Sequence[float], b: Sequence[float]) -> float:
    return sum(abs(x - y) for x, y in zip(a, b, strict=True)) / len(a)


def share_within(a: Sequence[float], b: Sequence[float], points: float) -> float:
    return sum(1 for x, y in zip(a, b, strict=True) if abs(x - y) <= points) / len(a)


def percentile(values: Sequence[float], p: float) -> float:
    """Nearest-rank percentile."""
    if not values:
        raise ValueError("no values")
    ordered = sorted(values)
    rank = max(1, math.ceil(p / 100 * len(ordered)))
    return ordered[rank - 1]
