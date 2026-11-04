"""Benchmark harness: metrics against hand-computed values, dataset integrity, and a run on synthetic-v1."""

from __future__ import annotations

import hashlib
import json
import shutil
from pathlib import Path

import pytest

from tier2_evaluator.adapters.reference import ReferenceEvaluator
from tier2_evaluator.bench import metrics, run

from .conftest import REPO

DATASET = REPO / "services" / "tier2-evaluator" / "bench" / "synthetic-v1"


def test_quadratic_weighted_kappa_matches_a_hand_computed_value() -> None:
    # Observed: (0,0) (1,1) (2,1) (2,2); expected from the marginals [1,1,2] x [1,2,1] / 4 -> 1 - 0.25 / 1.25.
    assert metrics.quadratic_weighted_kappa([0, 1, 2, 2], [0, 1, 1, 2], 3) == pytest.approx(0.8)
    assert metrics.quadratic_weighted_kappa([0, 1, 2], [0, 1, 2], 3) == pytest.approx(1.0)
    assert metrics.quadratic_weighted_kappa([0, 0, 4, 4], [4, 4, 0, 0]) < 0


def test_krippendorff_alpha_matches_the_published_example() -> None:
    # Krippendorff (2011), "Computing Krippendorff's alpha-reliability": four coders, twelve units, interval alpha 0.849.
    n = None
    a = [1, 2, 3, 3, 2, 1, 4, 1, 2, n, n, n]
    b = [1, 2, 3, 3, 2, 2, 4, 1, 2, 5, n, 3]
    c = [n, 3, 3, 3, 2, 3, 4, 2, 2, 5, 1, n]
    d = [1, 2, 3, 3, 2, 4, 4, 1, 2, 5, 1, n]
    assert metrics.krippendorff_alpha_interval(list(zip(a, b, c, d, strict=True))) == pytest.approx(0.849, abs=5e-4)
    assert metrics.krippendorff_alpha_interval([[1, 1], [2, 2], [3, 4]]) == pytest.approx(1 - (2 / 6) / (82 / 30))


def test_bands_and_summaries() -> None:
    assert [metrics.band(x) for x in (0, 19, 20, 59, 60, 79, 80, 100)] == [0, 0, 1, 2, 3, 3, 4, 4]
    with pytest.raises(ValueError):
        metrics.band(101)
    assert metrics.mean_absolute_error([10, 20], [20, 20]) == 5
    assert metrics.share_within([10, 20, 50], [21, 20, 20], 10) == pytest.approx(1 / 3)
    assert metrics.percentile([5, 1, 3, 2, 4], 50) == 3 and metrics.percentile([5, 1, 3, 2, 4], 95) == 5


def test_the_dataset_matches_its_manifest_and_never_splits_a_session() -> None:
    manifest, items = run.load_dataset(DATASET)
    assert manifest["synthetic"] is True and len(items) == len(manifest["items"]) == 15
    assert {i.language for i in items} == {"en", "tr"} and {i.split for i in items} == {"dev", "test"}


def test_a_tampered_or_leaking_dataset_is_refused(tmp_path: Path) -> None:
    copy = tmp_path / "ds"
    shutil.copytree(DATASET, copy)
    lines = (copy / "items.jsonl").read_text(encoding="utf-8").splitlines()
    tampered = json.loads(lines[0])
    tampered["labels"]["reference"]["technical_accuracy"] = 100
    (copy / "items.jsonl").write_text(
        "\n".join([json.dumps(tampered, ensure_ascii=False, sort_keys=True), *lines[1:]]) + "\n", encoding="utf-8"
    )
    with pytest.raises(run.DatasetError, match="manifest sha256"):
        run.load_dataset(copy)


def test_a_session_spanning_splits_is_refused(tmp_path: Path) -> None:
    copy = tmp_path / "ds"
    shutil.copytree(DATASET, copy)
    lines = (copy / "items.jsonl").read_text(encoding="utf-8").splitlines()
    first = json.loads(lines[0])
    first["split"] = "test" if first["split"] == "dev" else "dev"
    line = json.dumps(first, ensure_ascii=False, sort_keys=True)
    manifest = json.loads((copy / "manifest.json").read_text(encoding="utf-8"))
    manifest["items"][0]["sha256"] = hashlib.sha256(line.encode()).hexdigest()
    (copy / "manifest.json").write_text(json.dumps(manifest), encoding="utf-8")
    (copy / "items.jsonl").write_text("\n".join([line, *lines[1:]]) + "\n", encoding="utf-8")
    with pytest.raises(run.DatasetError, match="leakage"):
        run.load_dataset(copy)


async def test_report_counts_validity_abstention_and_agreement(monkeypatch: pytest.MonkeyPatch) -> None:
    manifest, items = run.load_dataset(DATASET)
    rubrics = run.load_rubrics(run.DEFAULT_RUBRICS)
    evaluator = ReferenceEvaluator()
    attempts = await run.evaluate_all(items, rubrics, evaluator)
    # Stand-in for the Go validator (exercised by make tier2-bench): the first evaluated attempt is
    # reported invalid, every other one valid.
    first = next(n for n, a in enumerate(attempts) if a.outcome == "evaluated")
    checks = [
        None if a.outcome != "evaluated" else {"valid": n != first, "problems": [] if n != first else ["quote"]}
        for n, a in enumerate(attempts)
    ]
    r = run.report(manifest, items, attempts, checks, evaluator)
    assert r["abstention"] == {"abstained": 1, "rate": round(1 / len(items), 3), "reasons": {"no_candidate_speech": 1}}
    assert r["evidence_validity"]["evaluated"] == len(items) - 1 and r["evidence_validity"]["valid"] == len(items) - 2
    assert r["evidence_validity"]["problems"] == {"quote": 1}
    for d in run.DIMENSIONS:
        assert r["dimensions"][d]["evaluator_vs_reference"]["n"] == len(items) - 2  # invalid and unlabelled items are not compared
        assert -1 <= r["dimensions"][d]["raters"]["alpha_interval"] <= 1
    assert r["evaluator"]["version"] == "reference@1" and r["dataset"]["synthetic"] is True
    assert "not evaluation quality" in r["notice"]
    text = run.summary(r)
    assert "evidence validity" in text and "technical_accuracy" in text


def test_injected_instructions_in_the_dataset_score_nothing() -> None:
    _, items = run.load_dataset(DATASET)
    injected = [i for i in items if any("Ignore all previous instructions" in s.text for s in i.segments)]
    assert len(injected) == 1 and injected[0].labels is not None and set(injected[0].labels.reference.values()) == {0}
