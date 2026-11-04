"""Provider model registry (ADR-021): which evaluator models this worker may be configured with.

A copy of contracts/models/model-registry.v1.json (tools/dev/sync-tier2-contracts.sh). Tier-2 and
Tier-1 are separate deployables, so each carries its own small copy of this check (ADR-001).
"""

from __future__ import annotations

import json
from collections.abc import Mapping
from dataclasses import dataclass
from datetime import date
from functools import cache
from importlib import resources

PURPOSE = "tier2_evaluator"
WARN_WITHIN_DAYS = 90


@dataclass(frozen=True)
class RegisteredModel:
    id: str
    provider: str
    status: str
    shutdown_on: date | None
    replacements: tuple[str, ...]


Registry = Mapping[str, RegisteredModel]


@cache
def registry() -> Registry:
    """Evaluator models of the packaged registry, keyed by id."""
    raw = json.loads(resources.files("tier2_evaluator.contracts").joinpath("model-registry.v1.json").read_text(encoding="utf-8"))
    return parse(raw)


def parse(raw: Mapping[str, object]) -> Registry:
    models = raw["models"]
    assert isinstance(models, list)
    return {
        m["id"]: RegisteredModel(
            m["id"],
            m["provider"],
            m["status"],
            date.fromisoformat(m["shutdown_on"]) if m["shutdown_on"] else None,
            tuple(m["replacements"]),
        )
        for m in models
        if m["purpose"] == PURPOSE
    }


def check_model(
    model_id: str,
    *,
    provider: str,
    today: date,
    strict: bool,
    production: bool,
    problems: list[str],
    warnings: list[str],
    models: Registry | None = None,
) -> None:
    """Append refusals to problems and notices to warnings for a configured evaluator model."""
    m = (registry() if models is None else models).get(model_id)
    if m is None or m.provider != provider:
        msg = f"ATI_TIER2_MODEL={model_id!r} is not a registered {provider} evaluator model (model registry, ADR-021)"
        (problems if strict else warnings).append(msg)
        return
    if m.shutdown_on is not None and today >= m.shutdown_on:
        problems.append(f"ATI_TIER2_MODEL={model_id!r} was shut down by the provider on {m.shutdown_on}; use {' or '.join(m.replacements)}")
        return
    if production and m.status != "verified":
        problems.append(
            f"ATI_TIER2_MODEL={model_id!r} is {m.status}; production requires a model verified against the live provider (ADR-021)"
        )
    if m.status == "deprecated" and m.shutdown_on is not None:
        days = (m.shutdown_on - today).days
        warnings.append(f"ATI_TIER2_MODEL={model_id!r} is deprecated and shuts down on {m.shutdown_on} ({days} days)")
