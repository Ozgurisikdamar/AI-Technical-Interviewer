"""Deterministic reference evaluator (ADR-022 decision 10).

It exercises the pipeline end to end without a model. It finds a synthetic rubric's indicator
phrases in the candidate's own words and cites each match verbatim. It knows nothing about
technical correctness: a match is reported as partially correct, and confidence stays at 0.5 and
uncalibrated. It works only on rubrics marked synthetic and is refused in production.
"""

from __future__ import annotations

import unicodedata
from typing import Any

from ..evidence import Segment
from . import Attempt, EvaluationTask

VERSION = "reference@1"
CONFIDENCE = 0.5
DIMENSIONS = ("technical_accuracy", "architecture_maturity", "edge_case_awareness", "claim_support")


def _fold(text: str) -> tuple[str, list[int]]:
    """Case-folded text with whitespace runs collapsed, plus, for each folded character, its index
    in text. Turkish dotted and dotless i fold to "i" so that indicators match regardless of case."""
    out: list[str] = []
    index: list[int] = []
    space = False
    for i, ch in enumerate(text):
        if ch.isspace():
            if not space:
                out.append(" ")
                index.append(i)
            space = True
            continue
        space = False
        for f in "i" if ch in "İIı" else ch.lower():
            out.append(f)
            index.append(i)
    return "".join(out), index


def _phrase(text: str) -> str:
    return _fold(unicodedata.normalize("NFC", text))[0].strip()


def _find(segments: tuple[Segment, ...], phrases: list[str]) -> tuple[Segment, str] | None:
    """The first segment containing one of the phrases, with the whole words of the match quoted
    exactly as the segment has them (after Unicode NFC, which validation also applies)."""
    for seg in segments:
        text = unicodedata.normalize("NFC", seg.text)
        folded, index = _fold(text)
        for phrase in phrases:
            at = folded.find(phrase)
            if at < 0 or not phrase:
                continue
            start, end = index[at], index[at + len(phrase) - 1] + 1
            while start > 0 and not text[start - 1].isspace():
                start -= 1
            while end < len(text) and not text[end].isspace():
                end += 1
            return seg, text[start:end]
    return None


def _indicators(criterion: dict[str, Any], language: str) -> list[str]:
    by_language: dict[str, list[str]] = criterion.get("indicators") or {}
    phrases = by_language.get(language) or [p for ps in by_language.values() for p in ps]
    return [p for p in (_phrase(x) for x in phrases) if p]


def evaluate_output(task: EvaluationTask) -> dict[str, Any]:
    answer, body = task.answer, task.rubric.body
    first = answer.segments[0]
    evidence: list[dict[str, Any]] = []
    matched = dict.fromkeys(DIMENSIONS, 0)
    total = dict.fromkeys(DIMENSIONS, 0)
    missing: list[str] = []
    for criterion in body["criteria"]:
        dim, cid, refs = criterion["dimension"], criterion["criterion_id"], list(criterion["reference_ids"])
        total[dim] += 1
        hit = _find(answer.segments, _indicators(criterion, task.language))
        if hit is None:
            missing.append(cid)
            evidence.append(
                {
                    "dimension": dim,
                    "transcript_start_ms": first.start_ms,
                    "transcript_end_ms": first.end_ms,
                    "transcript_segment_ids": [first.id],
                    "quote": "",
                    "claim": f"No indicator of criterion {cid} was found in the answer.",
                    "assessment": "not_addressed",
                    "reference_ids": refs,
                }
            )
            continue
        seg, quote = hit
        matched[dim] += 1
        evidence.append(
            {
                "dimension": dim,
                "transcript_start_ms": seg.start_ms,
                "transcript_end_ms": seg.end_ms,
                "transcript_segment_ids": [seg.id],
                "quote": quote,
                "claim": f"The answer uses an indicator phrase of criterion {cid}; its correctness is not assessed.",
                "assessment": "partially_correct",
                "reference_ids": refs,
            }
        )
    # Half-up rounding of the share of a dimension's criteria that were matched.
    scores = {d: (200 * matched[d] + total[d]) // (2 * total[d]) if total[d] else 0 for d in DIMENSIONS}
    weights = {d: round(float(body["weights"][d]) * 100) for d in DIMENSIONS}
    centi = sum(scores[d] * weights[d] for d in DIMENSIONS)
    confidences = [s.confidence for s in answer.segments if s.confidence is not None]
    return {
        "session_id": answer.session_id,
        "question_id": answer.item_id,
        "evaluation_version": f"{body['rubric_id']}@{body['version']}",
        "transcript_quality": {
            "score": round(sum(confidences) / len(confidences), 3) if confidences else 1.0,
            "language": task.language,
            "ambiguities": [],
            "missing_segments": answer.missing_segments,
        },
        "scores": scores,
        "weighted_score": centi / 100,
        "confidence": {
            "score": CONFIDENCE,
            "calibrated": False,
            "reason": "Reference evaluator: indicator phrases of a synthetic rubric were matched; this is not an assessment of quality.",
        },
        "evidence": evidence,
        "missing_concepts": missing,
        "contradictions": [],
        "unsupported_claims": [],
        "hallucination_risk": {"score": 0, "severity": "low", "evidence": []},
        "recommendation": "HUMAN_REVIEW",
        "review_required": True,
    }


class ReferenceEvaluator:
    provider = "reference"
    model_id: str | None = None
    version = VERSION
    prompt_sha256: str | None = None

    async def evaluate(self, task: EvaluationTask) -> Attempt:
        if not task.rubric.synthetic:
            return Attempt("abstained", reason="rubric_not_synthetic")
        if not task.answer.segments:
            return Attempt("abstained", reason="no_candidate_speech")
        return Attempt("evaluated", output=evaluate_output(task), latency_ms=0)

    async def aclose(self) -> None:
        return None
