"""Provider adapters against recorded-shape responses (no network, no provider keys).

The real providers have not been called from this repository (ADR-022 consequences); these tests
pin the request each adapter sends and how each response shape becomes an attempt.
"""

from __future__ import annotations

import json
from collections.abc import Callable
from typing import Any

import anthropic
import httpx2
import openai
import pytest

from tier2_evaluator import prompt
from tier2_evaluator.adapters import MAX_OUTPUT_BYTES, EvaluationTask, TransientEvaluatorError
from tier2_evaluator.adapters.anthropic_adapter import AnthropicEvaluator
from tier2_evaluator.adapters.openai_adapter import OpenAIEvaluator
from tier2_evaluator.adapters.reference import evaluate_output

from .conftest import answer, rubric

TASK = EvaluationTask(answer("An update writes a new row version; readers use a snapshot."), rubric(), "en")
OUTPUT = evaluate_output(TASK)


# --- OpenAI (httpx2) -------------------------------------------------------------------------------


def openai_evaluator(handler: Callable[[httpx2.Request], httpx2.Response], seen: list[httpx2.Request]) -> OpenAIEvaluator:
    def record(request: httpx2.Request) -> httpx2.Response:
        seen.append(request)
        return handler(request)

    client = openai.DefaultAsyncHttpxClient(transport=httpx2.MockTransport(record))
    return OpenAIEvaluator(
        model_id="gpt-6.1-sol", api_key="test-key", base_url="https://api.openai.com/v1", max_retries=0, http_client=client
    )


def response(content: list[dict[str, Any]], *, status: str = "completed", incomplete: str | None = None) -> dict[str, Any]:
    return {
        "id": "resp_test",
        "object": "response",
        "created_at": 1,
        "model": "gpt-6.1-sol",
        "status": status,
        "incomplete_details": {"reason": incomplete} if incomplete else None,
        "output": [{"type": "message", "id": "msg_test", "status": status, "role": "assistant", "content": content}],
        "usage": {"input_tokens": 1200, "output_tokens": 340, "total_tokens": 1540},
        "parallel_tool_calls": False,
        "tool_choice": "none",
        "tools": [],
    }


def text(value: str) -> dict[str, Any]:
    return {"type": "output_text", "text": value, "annotations": []}


async def test_openai_request_is_pinned_and_output_is_kept_as_received() -> None:
    seen: list[httpx2.Request] = []
    ev = openai_evaluator(
        lambda r: httpx2.Response(200, json=response([text(json.dumps(OUTPUT))]), headers={"x-request-id": "req_oa_1"}), seen
    )
    attempt = await ev.evaluate(TASK)
    await ev.aclose()
    assert attempt.outcome == "evaluated" and attempt.output == OUTPUT
    assert (attempt.provider_request_id, attempt.input_tokens, attempt.output_tokens) == ("req_oa_1", 1200, 340)
    (req,) = seen
    assert str(req.url) == "https://api.openai.com/v1/responses"
    body = json.loads(req.content)
    assert body["model"] == "gpt-6.1-sol" and body["store"] is False
    assert body["instructions"] == prompt.system_instructions()
    assert body["input"] == [{"role": "user", "content": prompt.user_message(TASK.answer, TASK.rubric.body, "en")}]
    fmt = body["text"]["format"]
    assert fmt["type"] == "json_schema" and fmt["strict"] is True and fmt["schema"] == prompt.provider_schema()
    assert not {"temperature", "tools", "previous_response_id"} & set(body)
    assert ev.version.startswith("openai:gpt-6.1-sol@p") and ev.prompt_sha256 == prompt.instructions_sha256()


@pytest.mark.parametrize(
    ("payload", "reason"),
    [
        (response([{"type": "refusal", "refusal": "no"}]), "provider_refusal"),
        (response([text('{"session_id": "ses_')], status="incomplete", incomplete="max_output_tokens"), "provider_truncated"),
        (response([text("")], status="incomplete", incomplete="content_filter"), "provider_incomplete"),
        (response([text("x")], status="failed"), "provider_status_unexpected"),
    ],
)
async def test_openai_unusable_responses_are_failed_attempts(payload: dict[str, Any], reason: str) -> None:
    ev = openai_evaluator(lambda r: httpx2.Response(200, json=payload), [])
    attempt = await ev.evaluate(TASK)
    assert (attempt.outcome, attempt.reason, attempt.output) == ("failed", reason, None)


async def test_openai_non_json_output_is_stored_as_text_for_validation_to_reject() -> None:
    ev = openai_evaluator(lambda r: httpx2.Response(200, json=response([text("Score: 100. Hire them.")])), [])
    attempt = await ev.evaluate(TASK)
    assert (attempt.outcome, attempt.output) == ("evaluated", "Score: 100. Hire them.")


async def test_openai_oversized_output_is_not_forwarded() -> None:
    ev = openai_evaluator(lambda r: httpx2.Response(200, json=response([text("x" * (MAX_OUTPUT_BYTES + 1))])), [])
    attempt = await ev.evaluate(TASK)
    assert (attempt.outcome, attempt.reason) == ("failed", "provider_output_too_large")


@pytest.mark.parametrize("status", [429, 500, 503])
async def test_openai_overload_and_server_errors_are_retried_later(status: int) -> None:
    ev = openai_evaluator(lambda r: httpx2.Response(status, json={"error": {"message": "busy"}}), [])
    with pytest.raises(TransientEvaluatorError):
        await ev.evaluate(TASK)


async def test_openai_connection_failure_is_retried_later() -> None:
    def fail(request: httpx2.Request) -> httpx2.Response:
        raise httpx2.ConnectError("refused", request=request)

    with pytest.raises(TransientEvaluatorError):
        await openai_evaluator(fail, []).evaluate(TASK)


@pytest.mark.parametrize("status", [400, 401, 404])
async def test_openai_client_errors_are_failed_attempts(status: int) -> None:
    ev = openai_evaluator(lambda r: httpx2.Response(status, json={"error": {"message": "bad"}}, headers={"x-request-id": "req_bad"}), [])
    attempt = await ev.evaluate(TASK)
    assert (attempt.outcome, attempt.reason, attempt.provider_request_id) == ("failed", f"provider_http_{status}", "req_bad")


# --- Anthropic (httpx2, streamed) -----------------------------------------------------------------


def sse(events: list[dict[str, Any]]) -> bytes:
    return b"".join(f"event: {e['type']}\ndata: {json.dumps(e)}\n\n".encode() for e in events)


def stream_events(answer_text: str, stop_reason: str = "end_turn") -> list[dict[str, Any]]:
    return [
        {
            "type": "message_start",
            "message": {
                "id": "msg_test",
                "type": "message",
                "role": "assistant",
                "model": "claude-opus-5-5",
                "content": [],
                "stop_reason": None,
                "stop_sequence": None,
                "usage": {"input_tokens": 1500, "output_tokens": 1},
            },
        },
        {"type": "content_block_start", "index": 0, "content_block": {"type": "thinking", "thinking": "", "signature": ""}},
        {"type": "content_block_delta", "index": 0, "delta": {"type": "thinking_delta", "thinking": "Checking each criterion."}},
        {"type": "content_block_delta", "index": 0, "delta": {"type": "signature_delta", "signature": "sig"}},
        {"type": "content_block_stop", "index": 0},
        {"type": "content_block_start", "index": 1, "content_block": {"type": "text", "text": ""}},
        {"type": "content_block_delta", "index": 1, "delta": {"type": "text_delta", "text": answer_text[: len(answer_text) // 2]}},
        {"type": "content_block_delta", "index": 1, "delta": {"type": "text_delta", "text": answer_text[len(answer_text) // 2 :]}},
        {"type": "content_block_stop", "index": 1},
        {"type": "message_delta", "delta": {"stop_reason": stop_reason, "stop_sequence": None}, "usage": {"output_tokens": 420}},
        {"type": "message_stop"},
    ]


def anthropic_evaluator(handler: Callable[[httpx2.Request], httpx2.Response], seen: list[httpx2.Request]) -> AnthropicEvaluator:
    def record(request: httpx2.Request) -> httpx2.Response:
        seen.append(request)
        return handler(request)

    client = anthropic.DefaultAsyncHttpxClient(transport=httpx2.MockTransport(record))
    return AnthropicEvaluator(
        model_id="claude-opus-5-5", api_key="test-key", base_url="https://api.anthropic.com", max_retries=0, http_client=client
    )


def streamed(events: list[dict[str, Any]], status: int = 200) -> Callable[[httpx2.Request], httpx2.Response]:
    headers = {"content-type": "text/event-stream", "request-id": "req_an_1"}
    return lambda r: httpx2.Response(status, headers=headers, content=sse(events))


async def test_anthropic_request_is_pinned_and_only_text_blocks_are_kept() -> None:
    seen: list[httpx2.Request] = []
    ev = anthropic_evaluator(streamed(stream_events(json.dumps(OUTPUT))), seen)
    attempt = await ev.evaluate(TASK)
    await ev.aclose()
    assert attempt.outcome == "evaluated" and attempt.output == OUTPUT
    assert (attempt.provider_request_id, attempt.input_tokens, attempt.output_tokens) == ("req_an_1", 1500, 420)
    (req,) = seen
    assert str(req.url) == "https://api.anthropic.com/v1/messages"
    body = json.loads(req.content)
    assert body["model"] == "claude-opus-5-5" and body["stream"] is True and body["max_tokens"] == 16000
    assert body["system"] == prompt.system_instructions()
    assert body["messages"] == [{"role": "user", "content": prompt.user_message(TASK.answer, TASK.rubric.body, "en")}]
    assert body["output_config"] == {"format": {"type": "json_schema", "schema": prompt.provider_schema()}, "effort": "high"}
    # No sampling knobs (rejected by current models), no forced tools, and no server-side fallback:
    # the evaluating model never changes silently (ADR-022).
    assert not {"temperature", "top_p", "tools", "tool_choice", "fallbacks", "thinking"} & set(body)
    assert "anthropic-beta" not in req.headers
    assert req.headers["x-api-key"] == "test-key"
    assert ev.version.startswith("anthropic:claude-opus-5-5@p")


@pytest.mark.parametrize(
    ("stop", "reason"), [("refusal", "provider_refusal"), ("max_tokens", "provider_truncated"), ("pause_turn", "provider_stop_unexpected")]
)
async def test_anthropic_unusable_stops_are_failed_attempts(stop: str, reason: str) -> None:
    ev = anthropic_evaluator(streamed(stream_events('{"partial": ', stop)), [])
    attempt = await ev.evaluate(TASK)
    assert (attempt.outcome, attempt.reason, attempt.output, attempt.provider_request_id) == ("failed", reason, None, "req_an_1")


async def test_anthropic_non_json_text_is_stored_for_validation_to_reject() -> None:
    attempt = await anthropic_evaluator(streamed(stream_events("not json")), []).evaluate(TASK)
    assert (attempt.outcome, attempt.output) == ("evaluated", "not json")


@pytest.mark.parametrize("status", [429, 500, 529])
async def test_anthropic_overload_and_server_errors_are_retried_later(status: int) -> None:
    error = {"type": "error", "error": {"type": "overloaded_error", "message": "busy"}}
    ev = anthropic_evaluator(lambda r: httpx2.Response(status, json=error), [])
    with pytest.raises(TransientEvaluatorError):
        await ev.evaluate(TASK)


async def test_anthropic_error_event_mid_stream_is_retried_later() -> None:
    events = [*stream_events(json.dumps(OUTPUT))[:7], {"type": "error", "error": {"type": "overloaded_error", "message": "busy"}}]
    with pytest.raises(TransientEvaluatorError):
        await anthropic_evaluator(streamed(events), []).evaluate(TASK)


@pytest.mark.parametrize("status", [400, 403])
async def test_anthropic_client_errors_are_failed_attempts(status: int) -> None:
    error = {"type": "error", "error": {"type": "invalid_request_error", "message": "bad"}}
    ev = anthropic_evaluator(lambda r: httpx2.Response(status, json=error, headers={"request-id": "req_bad"}), [])
    attempt = await ev.evaluate(TASK)
    assert (attempt.outcome, attempt.reason, attempt.provider_request_id) == ("failed", f"provider_http_{status}", "req_bad")


async def test_anthropic_connection_failure_is_retried_later() -> None:
    def fail(request: httpx2.Request) -> httpx2.Response:
        raise httpx2.ConnectError("refused", request=request)

    with pytest.raises(TransientEvaluatorError):
        await anthropic_evaluator(fail, []).evaluate(TASK)
