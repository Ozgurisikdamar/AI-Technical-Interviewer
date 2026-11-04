"""Evaluator adapters (ADR-022 decision 10).

An adapter turns one answer's evidence into one attempt: the evaluator's JSON exactly as received,
an abstention, or a failure. Adapters never decide whether output is acceptable; the control plane
validates every attempt against the stored transcript before anything becomes an evaluation.
"""

from __future__ import annotations

import json
import time
from dataclasses import dataclass
from typing import Any, Literal, Protocol

from ..config import Config
from ..evidence import AnswerEvidence, StoredRubric

# Bound for stored evaluator output (the control plane's MaxEvaluatorOutput).
MAX_OUTPUT_BYTES = 256 << 10


@dataclass(frozen=True)
class EvaluationTask:
    answer: AnswerEvidence
    rubric: StoredRubric
    language: str


@dataclass(frozen=True)
class Attempt:
    outcome: Literal["evaluated", "abstained", "failed"]
    # evaluated: the parsed JSON value, or the raw text when the provider returned something that is
    # not JSON (stored as a JSON string and rejected by validation).
    output: Any = None
    reason: str | None = None  # abstained or failed: a short machine reason
    provider_request_id: str | None = None
    latency_ms: int | None = None
    input_tokens: int | None = None
    output_tokens: int | None = None


class TransientEvaluatorError(Exception):
    """The provider could not be reached or asked us to slow down; the delivery is retried."""


def parsed_attempt(text: str, common: dict[str, Any]) -> Attempt:
    """An evaluated attempt carrying a model's output as received: parsed JSON, or the raw text."""
    if len(text.encode()) > MAX_OUTPUT_BYTES:
        return Attempt("failed", reason="provider_output_too_large", **common)
    try:
        output: Any = json.loads(text)
    except ValueError:
        output = text  # stored as received; validation rejects it
    return Attempt("evaluated", output=output, **common)


def elapsed_ms(started: float) -> int:
    return int((time.monotonic() - started) * 1000)


class Evaluator(Protocol):
    provider: str
    model_id: str | None
    version: str
    prompt_sha256: str | None

    async def evaluate(self, task: EvaluationTask) -> Attempt: ...

    async def aclose(self) -> None: ...


def build(cfg: Config) -> Evaluator:
    if cfg.evaluator == "reference":
        from .reference import ReferenceEvaluator

        return ReferenceEvaluator()
    assert cfg.model_id is not None and cfg.api_key is not None and cfg.base_url is not None
    if cfg.evaluator == "openai":
        from .openai_adapter import OpenAIEvaluator

        return OpenAIEvaluator(model_id=cfg.model_id, api_key=cfg.api_key, base_url=cfg.base_url)
    from .anthropic_adapter import AnthropicEvaluator

    return AnthropicEvaluator(model_id=cfg.model_id, api_key=cfg.api_key, base_url=cfg.base_url)
