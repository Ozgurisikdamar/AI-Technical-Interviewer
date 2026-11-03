"""Internal API client: credentials, fencing headers, retries and proxy isolation (ADR-016)."""

from __future__ import annotations

import json
from typing import Any

import httpx
import pytest

from tier1_voice_agent import control_plane as cp_mod
from tier1_voice_agent.control_plane import ControlPlane, ControlPlaneError

from .conftest import make_ctx

CLAIM = {
    "lease_id": "lea_1",
    "grant": "grant-secret",
    "fencing_token": 3,
    "expires_at_unix_ms": 1,
    "renew_interval_ms": 5000,
    "context": make_ctx(),
}


@pytest.fixture(autouse=True)
def no_backoff(monkeypatch: pytest.MonkeyPatch) -> None:
    async def instant(_: float) -> None:
        return None

    monkeypatch.setattr(cp_mod.asyncio, "sleep", instant)


def client(handler: Any) -> tuple[ControlPlane, list[httpx.Request]]:
    seen: list[httpx.Request] = []

    def record(request: httpx.Request) -> httpx.Response:
        seen.append(request)
        result: httpx.Response = handler(request)
        return result

    return ControlPlane("http://cp.invalid", "service-token", transport=httpx.MockTransport(record)), seen


def problem(status: int, code: str) -> httpx.Response:
    return httpx.Response(status, json={"code": code, "detail": "x"}, headers={"content-type": "application/problem+json"})


async def claimed(handler: Any) -> tuple[ControlPlane, list[httpx.Request]]:
    def route(req: httpx.Request) -> httpx.Response:
        if req.url.path == "/internal/v1/tier1/claims":
            return httpx.Response(201, json=CLAIM)
        result: httpx.Response = handler(req)
        return result

    c, seen = client(route)
    await c.claim("ses_0123456789abcdefAB", "n" * 43, "AJ_1", "agent-AJ_1")
    return c, seen


async def test_claim_sends_service_credential_only_and_stores_the_lease() -> None:
    c, seen = await claimed(lambda r: httpx.Response(500))
    req = seen[0]
    assert req.headers["authorization"] == "Bearer service-token"
    assert "x-ati-agent-grant" not in req.headers and "x-ati-fencing-token" not in req.headers
    assert json.loads(req.content) == {
        "session_id": "ses_0123456789abcdefAB",
        "claim_nonce": "n" * 43,
        "job_id": "AJ_1",
        "agent_identity": "agent-AJ_1",
    }
    assert c.lease is not None and c.lease.fencing_token == 3 and c.lease.grant == "grant-secret"
    await c.aclose()


async def test_session_requests_carry_grant_and_fencing_token() -> None:
    c, seen = await claimed(lambda r: httpx.Response(200, json=make_ctx()))
    await c.context()
    req = seen[-1]
    assert req.url.path == "/internal/v1/tier1/sessions/ses_0123456789abcdefAB/context"
    assert req.headers["x-ati-agent-grant"] == "grant-secret" and req.headers["x-ati-fencing-token"] == "3"
    await c.aclose()


async def test_session_request_without_lease_is_not_sent() -> None:
    c, seen = client(lambda r: httpx.Response(200, json={}))
    with pytest.raises(ControlPlaneError) as err:
        await c.context()
    assert err.value.code == "no_lease" and not seen
    await c.aclose()


async def test_transient_errors_are_retried_with_the_same_segment_key() -> None:
    answers = iter([httpx.Response(503), problem(502, "unavailable"), httpx.Response(201, json={"segment_id": "seg_1"})])
    c, seen = await claimed(lambda r: next(answers))
    res = await c.submit_segment({"segment_key": "AJ_1:c:000001", "text": "x"})
    assert res == {"segment_id": "seg_1"}
    bodies = [json.loads(r.content) for r in seen[1:]]
    assert len(bodies) == 3 and all(b["segment_key"] == "AJ_1:c:000001" for b in bodies)
    await c.aclose()


async def test_client_errors_are_not_retried() -> None:
    c, seen = await claimed(lambda r: problem(409, "segment_in_pause"))
    with pytest.raises(ControlPlaneError) as err:
        await c.submit_segment({"segment_key": "k"})
    assert (err.value.status, err.value.code) == (409, "segment_in_pause")
    assert len(seen) == 2  # claim + one attempt
    await c.aclose()


async def test_unreachable_control_plane_maps_to_status_zero() -> None:
    def fail(_: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("refused")

    c, _ = await claimed(fail)
    with pytest.raises(ControlPlaneError) as err:
        await c.context()
    assert (err.value.status, err.value.code) == (0, "unreachable")
    await c.aclose()


async def test_answer_complete_retries_while_the_manifest_is_incomplete() -> None:
    answers = iter(
        [
            problem(409, "manifest_incomplete"),
            problem(409, "manifest_incomplete"),
            httpx.Response(200, json=make_ctx("QUESTION_ACTIVE", version=9)),
        ]
    )
    c, seen = await claimed(lambda r: next(answers))
    ctx = await c.answer_complete(8, "act_0123456789abcdefAB", ["k1", "k2"], "agent_tool")
    assert ctx["state_version"] == 9
    assert json.loads(seen[-1].content) == {
        "state_version": 8,
        "activation_id": "act_0123456789abcdefAB",
        "flushed_segment_keys": ["k1", "k2"],
        "cause": "agent_tool",
    }
    await c.aclose()


async def test_answer_complete_gives_up_on_a_persistently_incomplete_manifest() -> None:
    c, _ = await claimed(lambda r: problem(409, "manifest_incomplete"))
    with pytest.raises(ControlPlaneError) as err:
        await c.answer_complete(8, "act_0123456789abcdefAB", ["k1"], "agent_tool")
    assert err.value.code == "manifest_incomplete"
    await c.aclose()


async def test_environment_proxies_are_never_used(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("HTTPS_PROXY", "http://proxy.invalid:9")
    monkeypatch.setenv("HTTP_PROXY", "http://proxy.invalid:9")
    monkeypatch.setenv("ALL_PROXY", "http://proxy.invalid:9")
    c = ControlPlane("http://cp.invalid", "t")
    assert c._client._trust_env is False
    assert not c._client._mounts  # no proxy transports mounted from the environment
    await c.aclose()


async def test_release_accepts_no_content() -> None:
    c, seen = await claimed(lambda r: httpx.Response(204))
    await c.release()
    assert seen[-1].url.path.endswith("/lease/release")
    await c.aclose()
