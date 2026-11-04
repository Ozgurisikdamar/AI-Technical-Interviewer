"""Offline benchmark run (DATA_AND_ML_LIFECYCLE sections 3 and 5).

    python -m tier2_evaluator.bench --validator PATH/TO/ati-admin [--dataset DIR] [--rubrics DIR] [--out FILE]

It runs in four steps:
1. Evaluate every item of a versioned dataset with the reference evaluator.
2. Validate every attempt with the control plane's own validator (`ati-admin validate-evaluations`), so that
   evidence validity is measured by the code that guards production.
3. Measure abstention, and score agreement with the reference labels, next to the agreement between raters.
4. Measure latency and token use.

On a synthetic dataset the numbers check the mechanics only.
"""

from __future__ import annotations

import argparse
import asyncio
import contextlib
import hashlib
import json
import os
import subprocess
import sys
import tempfile
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from ..adapters import Attempt, EvaluationTask, Evaluator
from ..adapters.reference import ReferenceEvaluator
from ..evidence import AnswerEvidence, Segment, StoredRubric
from ..worker import abstention
from . import metrics

DIMENSIONS = ("technical_accuracy", "architecture_maturity", "edge_case_awareness", "claim_support")
SERVICE = Path(__file__).resolve().parents[3]
REPO = SERVICE.parents[1]
DEFAULT_DATASET = SERVICE / "bench" / "synthetic-v1"
DEFAULT_RUBRICS = REPO / "contracts" / "content" / "rubrics"
SESSION = "ses_benchmark0000000000"

Scores = dict[str, int]


class DatasetError(Exception):
    """The dataset does not match its manifest or breaks a split rule."""


@dataclass(frozen=True)
class Labels:
    rater_a: Scores
    rater_b: Scores
    reference: Scores


@dataclass(frozen=True)
class Item:
    item_id: str
    session_key: str
    split: str
    language: str
    question_item: str
    rubric_id: str
    rubric_version: int
    segments: tuple[Segment, ...]
    labels: Labels | None


def load_dataset(directory: Path) -> tuple[dict[str, Any], list[Item]]:
    """Items of a dataset version, after checking every item against the manifest's sha256 and that no
    session spans splits (leakage)."""
    manifest: dict[str, Any] = json.loads((directory / "manifest.json").read_text(encoding="utf-8"))
    pins = {e["item_id"]: e["sha256"] for e in manifest["items"]}
    items: list[Item] = []
    for line in (directory / "items.jsonl").read_text(encoding="utf-8").splitlines():
        if not line.strip():
            continue
        rec = json.loads(line)
        if pins.get(rec["item_id"]) != hashlib.sha256(line.encode()).hexdigest():
            raise DatasetError(f"{rec['item_id']} does not match its manifest sha256")
        segments = tuple(Segment(s["segment_id"], "candidate", s["start_ms"], s["end_ms"], s["text"], None) for s in rec["segments"])
        raw = rec["labels"]
        labels = Labels(raw["rater_a"], raw["rater_b"], raw["reference"]) if raw else None
        items.append(
            Item(
                rec["item_id"],
                rec["session_key"],
                rec["split"],
                rec["language"],
                rec["question_item"],
                rec["rubric"]["rubric_id"],
                rec["rubric"]["version"],
                segments,
                labels,
            )
        )
    if sorted(i.item_id for i in items) != sorted(pins):
        raise DatasetError("the dataset and its manifest list different items")
    splits: dict[str, set[str]] = {}
    for i in items:
        splits.setdefault(i.session_key, set()).add(i.split)
    leaking = sorted(k for k, v in splits.items() if len(v) > 1)
    if leaking:
        raise DatasetError(f"sessions span splits (leakage): {', '.join(leaking)}")
    return manifest, items


def load_rubrics(directory: Path) -> dict[tuple[str, int], StoredRubric]:
    out: dict[tuple[str, int], StoredRubric] = {}
    for path in sorted(directory.glob("*.json")):
        body = json.loads(path.read_text(encoding="utf-8"))
        out[(body["rubric_id"], body["version"])] = StoredRubric(body["rubric_id"], body["version"], body["synthetic"], body)
    return out


def answer_of(item: Item) -> AnswerEvidence:
    return AnswerEvidence(
        SESSION,
        "ans_" + item.item_id.replace("-", ""),
        item.question_item,
        1,
        "",
        item.rubric_id,
        item.rubric_version,
        item.segments,
        False,
        (),
    )


async def evaluate_all(items: list[Item], rubrics: dict[tuple[str, int], StoredRubric], evaluator: Evaluator) -> list[Attempt]:
    """One attempt per item, through the worker's own abstention gates."""
    attempts: list[Attempt] = []
    for item in items:
        a = answer_of(item)
        reason = abstention(a)
        if reason is not None:
            attempts.append(Attempt("abstained", reason=reason))
            continue
        attempts.append(await evaluator.evaluate(EvaluationTask(a, rubrics[(item.rubric_id, item.rubric_version)], item.language)))
    return attempts


def validate(
    validator: Path, items: list[Item], rubrics: dict[tuple[str, int], StoredRubric], attempts: list[Attempt]
) -> list[dict[str, Any] | None]:
    """The control plane's validator (`ati-admin validate-evaluations`) on every evaluated attempt."""
    lines: list[str] = []
    index: list[int] = []
    for n, (item, attempt) in enumerate(zip(items, attempts, strict=True)):
        if attempt.outcome != "evaluated":
            continue
        a = answer_of(item)
        segments = [{"id": s.id, "speaker": s.speaker, "start_ms": s.start_ms, "end_ms": s.end_ms, "text": s.text} for s in a.segments]
        answer = {
            "session_id": a.session_id,
            "item_id": a.item_id,
            "transcript_version": a.transcript_version,
            "segments": segments,
            "missing_segments": a.missing_segments,
            "integrity_flags": list(a.integrity_flags),
        }
        rubric = rubrics[(item.rubric_id, item.rubric_version)].body
        lines.append(json.dumps({"answer": answer, "rubric": rubric, "output": attempt.output}, ensure_ascii=False))
        index.append(n)
    results: list[dict[str, Any] | None] = [None] * len(items)
    if not lines:
        return results
    fd, path = tempfile.mkstemp(suffix=".jsonl")
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            f.write("\n".join(lines) + "\n")
        out = subprocess.run(  # noqa: S603  (the operator's own validator binary)
            [str(validator), "validate-evaluations", "--input", path], check=True, capture_output=True, text=True, timeout=120
        )
    finally:
        with contextlib.suppress(OSError):
            os.unlink(path)
    for n, line in zip(index, out.stdout.splitlines(), strict=True):
        results[n] = json.loads(line)
    return results


def _dimension(items: list[Item], attempts: list[Attempt], scored: list[int], d: str) -> dict[str, Any]:
    labelled = [i.labels for i in items if i.labels is not None]
    rater_a = [lab.rater_a[d] for lab in labelled]
    rater_b = [lab.rater_b[d] for lab in labelled]
    ref = [items[n].labels.reference[d] for n in scored]  # type: ignore[union-attr]
    got = [int(attempts[n].output["scores"][d]) for n in scored]
    out: dict[str, Any] = {
        "raters": {
            "n": len(labelled),
            "kappa_quadratic_bands": round(
                metrics.quadratic_weighted_kappa([metrics.band(x) for x in rater_a], [metrics.band(x) for x in rater_b]), 3
            ),
            "alpha_interval": round(metrics.krippendorff_alpha_interval([[x, y] for x, y in zip(rater_a, rater_b, strict=True)]), 3),
        },
        "evaluator_vs_reference": {"n": len(ref), "mae": None, "within_10": None, "kappa_quadratic_bands": None},
    }
    if ref:
        out["evaluator_vs_reference"] = {
            "n": len(ref),
            "mae": round(metrics.mean_absolute_error(got, ref), 1),
            "within_10": round(metrics.share_within(got, ref, 10), 3),
            "kappa_quadratic_bands": round(
                metrics.quadratic_weighted_kappa([metrics.band(x) for x in got], [metrics.band(x) for x in ref]), 3
            ),
        }
    return out


def report(
    manifest: dict[str, Any], items: list[Item], attempts: list[Attempt], checks: list[dict[str, Any] | None], evaluator: Evaluator
) -> dict[str, Any]:
    evaluated = [n for n, a in enumerate(attempts) if a.outcome == "evaluated"]
    valid = [n for n in evaluated if (checks[n] or {}).get("valid") is True]
    problems: dict[str, int] = {}
    for n in evaluated:
        for p in (checks[n] or {}).get("problems", []):
            problems[p] = problems.get(p, 0) + 1
    abstained: dict[str, int] = {}
    for a in attempts:
        if a.outcome == "abstained":
            abstained[a.reason or "unknown"] = abstained.get(a.reason or "unknown", 0) + 1
    scored = [n for n in valid if items[n].labels is not None]  # only validated output is compared with labels
    latencies = [float(a.latency_ms) for a in attempts if a.latency_ms is not None]
    return {
        "dataset": {"name": manifest["dataset"], "version": manifest["version"], "synthetic": manifest["synthetic"], "items": len(items)},
        "evaluator": {"version": evaluator.version, "provider": evaluator.provider, "model_id": evaluator.model_id},
        "evidence_validity": {
            "evaluated": len(evaluated),
            "valid": len(valid),
            "rate": round(len(valid) / len(evaluated), 3) if evaluated else None,
            "problems": problems,
        },
        "abstention": {"abstained": sum(abstained.values()), "rate": round(sum(abstained.values()) / len(items), 3), "reasons": abstained},
        "dimensions": {d: _dimension(items, attempts, scored, d) for d in DIMENSIONS},
        "latency_ms": {"p50": metrics.percentile(latencies, 50), "p95": metrics.percentile(latencies, 95)} if latencies else None,
        "tokens": {"input": sum(a.input_tokens or 0 for a in attempts), "output": sum(a.output_tokens or 0 for a in attempts)},
        "notice": "Synthetic labels: these numbers check the harness, not evaluation quality." if manifest["synthetic"] else "",
    }


def summary(r: dict[str, Any]) -> str:
    v, ab, ds = r["evidence_validity"], r["abstention"], r["dataset"]
    lines = [
        f"dataset {ds['name']} v{ds['version']} ({ds['items']} items, synthetic: {ds['synthetic']}); evaluator {r['evaluator']['version']}",
        f"evidence validity {v['valid']}/{v['evaluated']}; abstained {ab['abstained']} {ab['reasons']}",
        f"{'dimension':<24}{'raters kappa':>13}{'raters alpha':>13}{'MAE':>7}{'within 10':>11}{'kappa':>7}",
    ]
    for d, m in r["dimensions"].items():
        e = m["evaluator_vs_reference"]
        lines.append(
            f"{d:<24}{m['raters']['kappa_quadratic_bands']:>13}{m['raters']['alpha_interval']:>13}{e['mae']!s:>7}{e['within_10']!s:>11}"
            f"{e['kappa_quadratic_bands']!s:>7}"
        )
    if r["notice"]:
        lines.append(r["notice"])
    return "\n".join(lines)


async def amain(argv: list[str] | None = None) -> int:
    p = argparse.ArgumentParser(prog="python -m tier2_evaluator.bench", description="Offline Tier-2 evaluator benchmark.")
    p.add_argument("--validator", type=Path, required=True, help="the ati-admin binary (validate-evaluations)")
    p.add_argument("--dataset", type=Path, default=DEFAULT_DATASET)
    p.add_argument("--rubrics", type=Path, default=DEFAULT_RUBRICS)
    p.add_argument("--out", type=Path, help="write the full report as JSON")
    p.add_argument("--require-valid", action="store_true", help="fail unless the validator accepts every evaluated attempt")
    args = p.parse_args(argv)
    manifest, items = load_dataset(args.dataset)
    rubrics = load_rubrics(args.rubrics)
    evaluator = ReferenceEvaluator()
    attempts = await evaluate_all(items, rubrics, evaluator)
    result = report(manifest, items, attempts, validate(args.validator, items, rubrics, attempts), evaluator)
    if args.out:
        args.out.write_text(json.dumps(result, indent=1, ensure_ascii=False) + "\n", encoding="utf-8")
    print(summary(result))
    v = result["evidence_validity"]
    if args.require_valid and v["valid"] != v["evaluated"]:
        print(f"validator rejected {v['evaluated'] - v['valid']} attempt(s): {v['problems']}", file=sys.stderr)
        return 1
    return 0


def main() -> None:
    sys.exit(asyncio.run(amain()))
