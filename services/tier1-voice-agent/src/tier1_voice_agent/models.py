"""Provider model registry (ADR-021): the model identifiers the agent may be configured with.

The registry is a copy of contracts/models/model-registry.v1.json (tools/dev/sync-agent-contracts.sh).
A model is "verified" only with recorded live evidence; none is, in this repository.
"""

from __future__ import annotations

import json
from collections.abc import Mapping
from dataclasses import dataclass
from datetime import date
from functools import cache
from importlib import resources

# A deprecated model is reported this many days before its shutdown, and refused from that day on.
WARN_WITHIN_DAYS = 90


@dataclass(frozen=True)
class RegisteredModel:
    id: str
    provider: str
    purpose: str
    status: str
    shutdown_on: date | None
    replacements: tuple[str, ...]


Registry = Mapping[tuple[str, str], RegisteredModel]


@cache
def registry() -> Registry:
    """The packaged registry, keyed by (purpose, id)."""
    raw = json.loads(resources.files("tier1_voice_agent.contracts").joinpath("model-registry.v1.json").read_text(encoding="utf-8"))
    return parse(raw)


def parse(raw: Mapping[str, object]) -> Registry:
    models = raw["models"]
    assert isinstance(models, list)
    out: dict[tuple[str, str], RegisteredModel] = {}
    for m in models:
        shutdown = date.fromisoformat(m["shutdown_on"]) if m["shutdown_on"] else None
        out[(m["purpose"], m["id"])] = RegisteredModel(
            m["id"], m["provider"], m["purpose"], m["status"], shutdown, tuple(m["replacements"])
        )
    return out


def check_model(
    key: str,
    model_id: str,
    *,
    provider: str,
    purpose: str,
    today: date,
    strict: bool,
    production: bool,
    problems: list[str],
    warnings: list[str],
    models: Registry | None = None,
) -> None:
    """Validate one configured model identifier, appending refusals to problems and notices to warnings.

    strict: an identifier missing from the registry is refused (staging, production) rather than
    reported (dev, test, where new snapshots are tried out).
    """
    m = (registry() if models is None else models).get((purpose, model_id))
    if m is None or m.provider != provider:
        msg = f"{key}={model_id!r} is not a registered {provider} {purpose} model (contracts/models/model-registry.v1.json, ADR-021)"
        (problems if strict else warnings).append(msg)
        return
    if m.shutdown_on is not None and today >= m.shutdown_on:
        problems.append(f"{key}={model_id!r} was shut down by the provider on {m.shutdown_on}; use {' or '.join(m.replacements)}")
        return
    if production and m.status != "verified":
        problems.append(f"{key}={model_id!r} is {m.status}; production requires a model verified against the live provider (ADR-021)")
    if m.status == "deprecated" and m.shutdown_on is not None:
        days = (m.shutdown_on - today).days
        soon = " soon" if days <= WARN_WITHIN_DAYS else ""
        replace_with = " or ".join(m.replacements)
        warnings.append(
            f"{key}={model_id!r} is deprecated and shuts down{soon} on {m.shutdown_on} ({days} days); replace it with {replace_with}"
        )
