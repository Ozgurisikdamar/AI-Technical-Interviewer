"""Contract-level Tier-1/Tier-2 isolation checks (BLUEPRINT 3.1, ADR-001, ADR-015)."""

from __future__ import annotations

import re
from collections.abc import Iterator
from typing import Any

from conftest import SCHEMAS, load_json

FORBIDDEN_FOR_TIER1 = re.compile(
    r"score|rubric|evaluat|review|weight|confidence|recommend|hiring|rank|honest|cheat|emotion|personality|plan_items|candidate_ref",
    re.I,
)


def _property_names(schema: Any) -> Iterator[str]:
    if isinstance(schema, dict):
        for key, value in schema.items():
            if key == "properties" and isinstance(value, dict):
                yield from value.keys()
            yield from _property_names(value)
    elif isinstance(schema, list):
        for item in schema:
            yield from _property_names(item)


def _all_objects_closed(schema: Any, path: str = "$") -> Iterator[str]:
    if isinstance(schema, dict):
        if schema.get("type") == "object" or "properties" in schema:
            if "properties" in schema and schema.get("additionalProperties") is not False:
                yield path
        for key, value in schema.items():
            yield from _all_objects_closed(value, f"{path}.{key}")
    elif isinstance(schema, list):
        for i, item in enumerate(schema):
            yield from _all_objects_closed(item, f"{path}[{i}]")


def test_tier1_context_contains_no_evaluation_concepts() -> None:
    names = set(_property_names(load_json(SCHEMAS["tier1-context"])))
    leaked = sorted(n for n in names if FORBIDDEN_FOR_TIER1.search(n))
    assert not leaked, f"Tier-1 context exposes forbidden fields: {leaked}"


def test_tier1_context_closes_every_nested_object() -> None:
    open_objects = list(_all_objects_closed(load_json(SCHEMAS["tier1-context"])))
    assert not open_objects, f"objects accepting unknown keys: {open_objects}"


def test_tier1_context_never_exposes_post_interview_states() -> None:
    states = set(load_json(SCHEMAS["tier1-context"])["properties"]["state"]["enum"])
    assert not states & {"EVALUATION_PENDING", "HUMAN_REVIEW", "REPORT_READY"}, (
        "Tier-1 must not learn evaluation or review outcomes; collapse them to INTERVIEW_COMPLETED"
    )


def test_tier1_context_marks_candidate_text_as_untrusted() -> None:
    schema = load_json(SCHEMAS["tier1-context"])
    item = schema["properties"]["pending_text_inputs"]["items"]
    assert set(item["properties"]) == {"segment_id", "activation_id", "untrusted_text"}


def test_agent_cannot_attribute_segments_itself() -> None:
    props = set(load_json(SCHEMAS["segment-submission"])["properties"])
    assert not props & {"question_id", "activation_id", "turn_id", "sequence", "evidence_eligible", "transcript_version"}
    sources = set(load_json(SCHEMAS["segment-submission"])["properties"]["source"]["enum"])
    assert "candidate_text" not in sources, "only the control plane writes candidate text segments"


def test_handoff_events_carry_identifiers_only() -> None:
    for name in ("answer-finalized", "interview-completed"):
        names = set(_property_names(load_json(SCHEMAS[name])))
        assert not {"text", "quote", "transcript", "untrusted_text"} & names, name
        assert not [n for n in names if re.search(r"delay|latency|timing|pause_ms|score", n)], name
        assert not list(_all_objects_closed(load_json(SCHEMAS[name]))), name


def test_evaluation_contract_has_no_hiring_decision() -> None:
    schema = load_json(SCHEMAS["evaluation"])
    assert set(schema["properties"]["recommendation"]["enum"]) == {"NEXT_QUESTION", "FOLLOW_UP", "HUMAN_REVIEW"}
    assert not [n for n in _property_names(schema) if re.search(r"hire|reject|decision|honest", n)]
