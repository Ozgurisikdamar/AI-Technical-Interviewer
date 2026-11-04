"""Schema meta-validation and valid/invalid fixture tests (P&E section 8: schema valid/invalid fixtures)."""

from __future__ import annotations

from pathlib import Path

import pytest
from jsonschema import Draft202012Validator

from conftest import CONTRACTS, REPO, SCHEMAS, load_json, validator_for

FIXTURES = CONTRACTS / "fixtures"


def _fixtures(kind: str) -> list[tuple[str, Path]]:
    out = []
    for name in SCHEMAS:
        for path in sorted((FIXTURES / name / kind).glob("*.json")):
            out.append((name, path))
    return out


@pytest.mark.parametrize("name", sorted(SCHEMAS))
def test_schema_is_valid_draft_2020_12(name: str) -> None:
    schema = load_json(SCHEMAS[name])
    assert schema["$schema"] == "https://json-schema.org/draft/2020-12/schema"
    Draft202012Validator.check_schema(schema)


@pytest.mark.parametrize("name", sorted(SCHEMAS))
def test_schema_closes_top_level_object(name: str) -> None:
    schema = load_json(SCHEMAS[name])
    assert schema.get("additionalProperties") is False, f"{name} must reject unknown top-level properties"


@pytest.mark.parametrize("name", sorted(SCHEMAS))
def test_every_schema_has_valid_and_invalid_fixtures(name: str) -> None:
    has_content = name in ("interview-plan", "consent-notice")
    valid = list((FIXTURES / name / "valid").glob("*.json"))
    invalid = list((FIXTURES / name / "invalid").glob("*.json"))
    assert invalid, f"{name} needs invalid fixtures"
    assert valid or has_content, f"{name} needs valid fixtures"


@pytest.mark.parametrize(("name", "path"), _fixtures("valid"), ids=lambda v: getattr(v, "name", v))
def test_valid_fixture_validates(name: str, path: Path) -> None:
    errors = list(validator_for(name).iter_errors(load_json(path)))
    assert not errors, [e.message for e in errors]


@pytest.mark.parametrize(("name", "path"), _fixtures("invalid"), ids=lambda v: getattr(v, "name", v))
def test_invalid_fixture_is_rejected(name: str, path: Path) -> None:
    assert list(validator_for(name).iter_errors(load_json(path))), f"{path.name} unexpectedly validated"


@pytest.mark.parametrize("path", sorted((CONTRACTS / "content/plans").glob("*.json")), ids=lambda p: p.name)
def test_shipped_plans_validate(path: Path) -> None:
    plan = load_json(path)
    errors = list(validator_for("interview-plan").iter_errors(plan))
    assert not errors, [e.message for e in errors]
    ids = [i["item_id"] for i in plan["items"]] + [f["item_id"] for i in plan["items"] for f in i["follow_ups"]]
    assert len(ids) == len(set(ids)), "item ids (including follow-ups) must be unique within a plan"
    assert path.name == f"{plan['plan_id']}.v{plan['version']}.json"
    # Nothing shipped here is a validated assessment: every plan must say it is synthetic, which
    # the control plane carries into audit records and integrity flags (ADR-020).
    assert plan["synthetic"] is True, f"{path.name} must be marked synthetic"
    assert plan["plan_id"].startswith("plan_demo_"), "shipped plans are demo plans"


@pytest.mark.parametrize("path", sorted((CONTRACTS / "content/notices").glob("*.json")), ids=lambda p: p.name)
def test_shipped_notices_validate_and_are_marked_draft(path: Path) -> None:
    notice = load_json(path)
    errors = list(validator_for("consent-notice").iter_errors(notice))
    assert not errors, [e.message for e in errors]
    # Nothing in this repository is legally approved: every shipped notice must stay draft
    # and keep an undetermined basis until legal review (ADR-020, TM section 4).
    assert notice["status"] == "draft"
    assert all(p["basis"] == "to_be_determined_by_legal" for p in notice["purposes"])
    assert path.name == f"{notice['notice_id']}.{notice['version']}.{notice['language']}.json"


def test_notice_languages_offer_identical_purposes() -> None:
    by_version: dict[tuple[str, str], dict[str, list[tuple[str, bool, str, tuple[str, ...]]]]] = {}
    for path in sorted((CONTRACTS / "content/notices").glob("*.json")):
        n = load_json(path)
        shape = sorted(
            (p["purpose_id"], p["required"], p["retention_category"], tuple(p["data_categories"])) for p in n["purposes"]
        )
        by_version.setdefault((n["notice_id"], n["version"]), {})[n["language"]] = shape
    assert by_version, "no notices found"
    for key, variants in by_version.items():
        assert len(variants) >= 2, f"{key}: expected en and tr translations"
        shapes = list(variants.values())
        assert all(s == shapes[0] for s in shapes), f"{key}: translations must offer identical purposes"


def test_audio_recording_purpose_is_optional() -> None:
    for path in (CONTRACTS / "content/notices").glob("*.json"):
        purposes = {p["purpose_id"]: p for p in load_json(path)["purposes"]}
        assert purposes["audio_recording"]["required"] is False, "recording must never be a precondition for the interview"
        assert purposes["voice_interview_processing"]["required"] is False, "text mode must remain available without voice processing"


def test_shipped_model_registry_is_consistent() -> None:
    """ADR-021: every configurable provider model is listed; replacements resolve within the same
    purpose; a model is verified only with evidence that exists in the repository."""
    registry = load_json(CONTRACTS / "models/model-registry.v1.json")
    errors = list(validator_for("model-registry").iter_errors(registry))
    assert not errors, [e.message for e in errors]
    keys = [(m["provider"], m["id"]) for m in registry["models"]]
    assert len(keys) == len(set(keys)), "a model is listed once per provider"
    by_id = {m["id"]: m for m in registry["models"]}
    for m in registry["models"]:
        for r in m["replacements"]:
            assert r in by_id, f"{m['id']}: replacement {r} is not in the registry"
            assert by_id[r]["purpose"] == m["purpose"], f"{m['id']}: replacement {r} serves another purpose"
            assert by_id[r]["status"] != "deprecated" or by_id[r]["shutdown_on"] > m["shutdown_on"], f"{m['id']}: replacement {r} retires first"
        if m["status"] == "verified":
            assert (REPO / m["evidence"]).is_file(), f"{m['id']}: evidence {m['evidence']} is missing"


DOCUMENTED_WEIGHTS = {"technical_accuracy": 0.40, "architecture_maturity": 0.25, "edge_case_awareness": 0.20, "claim_support": 0.15}


@pytest.mark.parametrize("path", sorted((CONTRACTS / "content/rubrics").glob("*.json")), ids=lambda p: p.name)
def test_shipped_rubrics_are_consistent(path: Path) -> None:
    """ADR-022: rubric weights equal the documented ones (the backend recomputes with them); every
    dimension has a criterion; criteria cite only the rubric's references; nothing shipped is validated."""
    rubric = load_json(path)
    errors = list(validator_for("rubric").iter_errors(rubric))
    assert not errors, [e.message for e in errors]
    assert path.name == f"{rubric['rubric_id']}.v{rubric['version']}.json"
    assert rubric["weights"] == DOCUMENTED_WEIGHTS
    assert {c["dimension"] for c in rubric["criteria"]} == set(DOCUMENTED_WEIGHTS)
    ids = [c["criterion_id"] for c in rubric["criteria"]]
    assert len(ids) == len(set(ids))
    refs = {r["reference_id"] for r in rubric["references"]}
    assert len(refs) == len(rubric["references"])
    for c in rubric["criteria"]:
        assert set(c["reference_ids"]) <= refs, f"{c['criterion_id']} cites an unknown reference"
        if rubric["synthetic"]:
            assert set(c["indicators"]) == set(rubric["languages"]), f"{c['criterion_id']} needs indicators for every language"
    assert rubric["synthetic"] is True and rubric["status"] == "draft" and rubric["rubric_id"].startswith("rub_demo_")


def test_every_plan_rubric_reference_resolves_to_a_shipped_rubric() -> None:
    shipped = {(r["rubric_id"], r["version"]) for r in map(load_json, (CONTRACTS / "content/rubrics").glob("*.json"))}
    for path in (CONTRACTS / "content/plans").glob("*.json"):
        plan = load_json(path)
        for item in plan["items"]:
            ref = item["rubric_ref"]
            if ref is not None:
                assert (ref["rubric_id"], ref["version"]) in shipped, f"{path.name} {item['item_id']} references a missing rubric"
                rubric = load_json(CONTRACTS / f"content/rubrics/{ref['rubric_id']}.v{ref['version']}.json")
                assert plan["language"] in rubric["languages"], f"{path.name} {item['item_id']}: rubric does not cover {plan['language']}"
