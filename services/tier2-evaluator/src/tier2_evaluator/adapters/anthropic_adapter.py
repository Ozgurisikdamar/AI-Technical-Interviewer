"""Anthropic evaluator: Messages API with structured outputs (ADR-021, ADR-022).

Text only, streamed so that a long evaluation does not hit a request timeout. The model ID and the
endpoint are pinned by configuration; credentials are passed explicitly, so the SDK reads no
credential, endpoint or header settings from the environment. Server-side refusal fallbacks are
deliberately not enabled: they would let another model produce the evaluation. A refusal is
recorded as a failed attempt and the answer goes to human review.
"""

from __future__ import annotations

import time
from typing import Any, Literal

import anthropic
from anthropic.types import OutputConfigParam

from .. import prompt
from . import Attempt, EvaluationTask, TransientEvaluatorError, elapsed_ms, parsed_attempt

# Error types worth retrying later. A stream can also fail after it started, with HTTP 200 and an
# error event, so the error body is checked as well as the status.
_TRANSIENT_TYPES = {"overloaded_error", "rate_limit_error", "api_error", "timeout_error"}


def _transient(e: anthropic.APIStatusError) -> bool:
    if e.status_code == 429 or e.status_code >= 500:
        return True
    body = e.body if isinstance(e.body, dict) else {}
    error = body.get("error")
    return isinstance(error, dict) and error.get("type") in _TRANSIENT_TYPES


class AnthropicEvaluator:
    provider = "anthropic"

    def __init__(
        self,
        *,
        model_id: str,
        api_key: str,
        base_url: str,
        timeout: float = 600.0,
        max_tokens: int = 16000,
        effort: Literal["low", "medium", "high", "xhigh", "max"] = "high",
        max_retries: int = 2,
        http_client: Any = None,
    ) -> None:
        self.model_id: str | None = model_id
        self.version = prompt.model_evaluator_version(self.provider, model_id)
        self.prompt_sha256: str | None = prompt.instructions_sha256()
        self._max_tokens, self._effort = max_tokens, effort
        self._client = anthropic.AsyncAnthropic(
            api_key=api_key, base_url=base_url, timeout=timeout, max_retries=max_retries, http_client=http_client
        )

    async def aclose(self) -> None:
        await self._client.close()

    def _output_config(self) -> OutputConfigParam:
        return {"format": {"type": "json_schema", "schema": prompt.provider_schema()}, "effort": self._effort}

    async def evaluate(self, task: EvaluationTask) -> Attempt:
        assert self.model_id is not None
        started = time.monotonic()
        request_id: str | None = None
        try:
            async with self._client.messages.stream(
                model=self.model_id,
                max_tokens=self._max_tokens,
                system=prompt.system_instructions(),
                messages=[{"role": "user", "content": prompt.user_message(task.answer, task.rubric.body, task.language)}],
                output_config=self._output_config(),
            ) as stream:
                request_id = stream.request_id
                msg = await stream.get_final_message()
        except anthropic.APIConnectionError as e:
            raise TransientEvaluatorError(type(e).__name__) from None
        except anthropic.APIStatusError as e:
            if _transient(e):
                raise TransientEvaluatorError(type(e).__name__) from None
            return Attempt(
                "failed",
                reason=f"provider_http_{e.status_code}",
                provider_request_id=e.request_id or request_id,
                latency_ms=elapsed_ms(started),
            )
        common: dict[str, Any] = {
            "provider_request_id": request_id,
            "latency_ms": elapsed_ms(started),
            "input_tokens": msg.usage.input_tokens,
            "output_tokens": msg.usage.output_tokens,
        }
        if msg.stop_reason == "refusal":
            return Attempt("failed", reason="provider_refusal", **common)
        if msg.stop_reason == "max_tokens":
            return Attempt("failed", reason="provider_truncated", **common)
        if msg.stop_reason != "end_turn":
            return Attempt("failed", reason="provider_stop_unexpected", **common)
        return parsed_attempt("".join(b.text for b in msg.content if b.type == "text"), common)
