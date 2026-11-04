"""OpenAI evaluator: Responses API with Structured Outputs (ADR-021, ADR-022).

Text only. The response is not stored by the provider (store=false). The model ID is pinned by
configuration and checked against the model registry; the endpoint is pinned too, so the SDK never
falls back to OPENAI_BASE_URL.
"""

from __future__ import annotations

import time
from typing import Any

import openai

from .. import prompt
from . import Attempt, EvaluationTask, TransientEvaluatorError, elapsed_ms, parsed_attempt


class OpenAIEvaluator:
    provider = "openai"

    def __init__(
        self,
        *,
        model_id: str,
        api_key: str,
        base_url: str,
        timeout: float = 300.0,
        max_output_tokens: int = 16000,
        max_retries: int = 2,
        http_client: Any = None,
    ) -> None:
        self.model_id: str | None = model_id
        self.version = prompt.model_evaluator_version(self.provider, model_id)
        self.prompt_sha256: str | None = prompt.instructions_sha256()
        self._max_output_tokens = max_output_tokens
        self._client = openai.AsyncOpenAI(
            api_key=api_key, base_url=base_url, timeout=timeout, max_retries=max_retries, http_client=http_client
        )

    async def aclose(self) -> None:
        await self._client.close()

    async def evaluate(self, task: EvaluationTask) -> Attempt:
        assert self.model_id is not None
        started = time.monotonic()
        try:
            resp = await self._client.responses.create(
                model=self.model_id,
                instructions=prompt.system_instructions(),
                input=[{"role": "user", "content": prompt.user_message(task.answer, task.rubric.body, task.language)}],
                text={
                    "format": {"type": "json_schema", "name": "interview_evaluation_v1", "schema": prompt.provider_schema(), "strict": True}
                },
                max_output_tokens=self._max_output_tokens,
                store=False,
            )
        except openai.APIConnectionError as e:
            raise TransientEvaluatorError(type(e).__name__) from None
        except openai.APIStatusError as e:
            if e.status_code == 429 or e.status_code >= 500:
                raise TransientEvaluatorError(type(e).__name__) from None
            return Attempt(
                "failed", reason=f"provider_http_{e.status_code}", provider_request_id=e.request_id, latency_ms=elapsed_ms(started)
            )
        common: dict[str, Any] = {
            "provider_request_id": resp._request_id,
            "latency_ms": elapsed_ms(started),
            "input_tokens": resp.usage.input_tokens if resp.usage else None,
            "output_tokens": resp.usage.output_tokens if resp.usage else None,
        }
        refused = any(c.type == "refusal" for item in resp.output if item.type == "message" for c in item.content)
        if refused:
            return Attempt("failed", reason="provider_refusal", **common)
        if resp.status == "incomplete":
            cause = resp.incomplete_details.reason if resp.incomplete_details else None
            return Attempt("failed", reason="provider_truncated" if cause == "max_output_tokens" else "provider_incomplete", **common)
        if resp.status != "completed":
            return Attempt("failed", reason="provider_status_unexpected", **common)
        return parsed_attempt(resp.output_text, common)
