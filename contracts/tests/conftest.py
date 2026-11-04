"""Shared helpers for contract tests."""

from __future__ import annotations

import json
import re
from pathlib import Path
from typing import Any

import pytest
from jsonschema import Draft202012Validator

REPO = Path(__file__).resolve().parents[2]
CONTRACTS = REPO / "contracts"
DOCS = REPO / "docs" / "architecture"

SCHEMAS: dict[str, Path] = {
    "evaluation": CONTRACTS / "schemas/evaluation/v1/evaluation.schema.json",
    "transcript-segment": CONTRACTS / "schemas/transcript/v1/transcript-segment.schema.json",
    "segment-submission": CONTRACTS / "schemas/transcript/v1/segment-submission.schema.json",
    "tier1-context": CONTRACTS / "schemas/tier1/v1/tier1-context.schema.json",
    "interview-plan": CONTRACTS / "schemas/plan/v1/interview-plan.schema.json",
    "consent-notice": CONTRACTS / "schemas/consent/v1/consent-notice.schema.json",
    "answer-finalized": CONTRACTS / "schemas/events/v1/answer-finalized.schema.json",
    "interview-completed": CONTRACTS / "schemas/events/v1/interview-completed.schema.json",
    "model-registry": CONTRACTS / "schemas/models/v1/model-registry.schema.json",
}


def load_json(path: Path) -> Any:
    return json.loads(path.read_text(encoding="utf-8"))


def validator_for(name: str) -> Draft202012Validator:
    schema = load_json(SCHEMAS[name])
    return Draft202012Validator(schema, format_checker=Draft202012Validator.FORMAT_CHECKER)


def doc_section(doc: str, heading_prefix: str) -> str:
    """Return the body of the level-2 section whose heading starts with heading_prefix."""
    m = re.search(rf"^## {re.escape(heading_prefix)}.*?$(.*?)(?=^## |\Z)", doc, re.S | re.M)
    if not m:
        raise AssertionError(f"section '## {heading_prefix}' not found")
    return m.group(1)


def first_fenced_block(text: str, lang: str) -> str:
    m = re.search(rf"```{lang}\n(.*?)```", text, re.S)
    if not m:
        raise AssertionError(f"no ```{lang} block found")
    return m.group(1)


@pytest.fixture(scope="session")
def state_machine() -> dict[str, Any]:
    return load_json(CONTRACTS / "state-machine/interview-session.v1.1.json")
