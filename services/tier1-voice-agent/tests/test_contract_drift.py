"""Packaged contracts must equal the repository contracts (tools/dev/sync-agent-contracts.sh)."""

from __future__ import annotations

from importlib import resources

import pytest

from .conftest import CONTRACTS

PAIRS = {
    "tier1-interviewer.v1.txt": "prompts/tier1-interviewer/v1/system.txt",
    "prompts-manifest.json": "prompts/manifest.json",
    "tier1-context.v1.schema.json": "schemas/tier1/v1/tier1-context.schema.json",
    "segment-submission.v1.schema.json": "schemas/transcript/v1/segment-submission.schema.json",
    "model-registry.v1.json": "models/model-registry.v1.json",
}


@pytest.mark.parametrize(("packaged", "source"), sorted(PAIRS.items()))
def test_packaged_contract_matches_repository(packaged: str, source: str) -> None:
    pkg = resources.files("tier1_voice_agent.contracts").joinpath(packaged).read_bytes()
    assert pkg == (CONTRACTS / source).read_bytes(), f"{packaged} drifted; run tools/dev/sync-agent-contracts.sh"


def test_no_tier2_material_is_packaged() -> None:
    names = {p.name for p in resources.files("tier1_voice_agent.contracts").iterdir()}
    assert not {n for n in names if "tier2" in n or "evaluation" in n or "rubric" in n}
