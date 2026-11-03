"""Schema meta-validation and valid/invalid fixture tests (P&E section 8: schema valid/invalid fixtures)."""

from __future__ import annotations

from pathlib import Path

import pytest
from jsonschema import Draft202012Validator

from conftest import CONTRACTS, SCHEMAS, load_json, validator_for

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
