"""Client for the control plane's internal Tier-1 API (ADR-016).

Every request carries the service credential; session requests also carry the per-session grant
and the current fencing token. Proxies are never inherited from the environment (ADR-011).
"""

from __future__ import annotations

import asyncio
import logging
from dataclasses import dataclass
from typing import Any

import httpx

log = logging.getLogger("tier1.control_plane")

_RETRYABLE = {502, 503, 504}


class ControlPlaneError(Exception):
    def __init__(self, status: int, code: str, detail: str = "") -> None:
        super().__init__(f"{status} {code}")
        self.status, self.code, self.detail = status, code, detail


@dataclass
class Lease:
    lease_id: str
    grant: str
    fencing_token: int
    expires_at_unix_ms: int
    renew_interval_ms: int


class ControlPlane:
    def __init__(
        self,
        base_url: str,
        service_token: str,
        *,
        proxy: str | None = None,
        timeout: float = 5.0,
        transport: httpx.AsyncBaseTransport | None = None,
    ) -> None:
        self._client = httpx.AsyncClient(base_url=base_url, timeout=timeout, trust_env=False, proxy=proxy, transport=transport)
        self._service_token = service_token
        self.session_id: str | None = None
        self.lease: Lease | None = None

    async def aclose(self) -> None:
        await self._client.aclose()

    def _headers(self, with_lease: bool) -> dict[str, str]:
        h = {"Authorization": f"Bearer {self._service_token}"}
        if with_lease:
            if self.lease is None:
                raise ControlPlaneError(0, "no_lease")
            h["X-ATI-Agent-Grant"] = self.lease.grant
            h["X-ATI-Fencing-Token"] = str(self.lease.fencing_token)
        return h

    async def _request(self, method: str, path: str, *, body: Any = None, with_lease: bool = True, attempts: int = 4) -> Any:
        delay = 0.2
        for attempt in range(attempts):
            try:
                resp = await self._client.request(method, path, json=body, headers=self._headers(with_lease))
            except httpx.TransportError:
                if attempt == attempts - 1:
                    raise ControlPlaneError(0, "unreachable") from None
            else:
                if resp.status_code < 400:
                    return resp.json() if resp.content else None
                code, detail = "error", ""
                try:
                    problem = resp.json()
                    code, detail = problem.get("code", "error"), problem.get("detail", "")
                except ValueError:
                    pass
                if resp.status_code not in _RETRYABLE or attempt == attempts - 1:
                    raise ControlPlaneError(resp.status_code, code, detail)
            await asyncio.sleep(delay)
            delay = min(delay * 2, 2.0)
        raise ControlPlaneError(0, "unreachable")

    def _session(self, suffix: str) -> str:
        return f"/internal/v1/tier1/sessions/{self.session_id}/{suffix}"

    async def claim(self, session_id: str, nonce: str, job_id: str, identity: str) -> dict[str, Any]:
        res = await self._request(
            "POST",
            "/internal/v1/tier1/claims",
            with_lease=False,
            body={"session_id": session_id, "claim_nonce": nonce, "job_id": job_id, "agent_identity": identity},
        )
        self.session_id = session_id
        self.lease = Lease(res["lease_id"], res["grant"], res["fencing_token"], res["expires_at_unix_ms"], res["renew_interval_ms"])
        ctx: dict[str, Any] = res["context"]
        return ctx

    async def renew(self) -> dict[str, Any]:
        res = await self._request("POST", self._session("lease/renew"))
        ctx: dict[str, Any] = res["context"]
        return ctx

    async def release(self) -> None:
        await self._request("POST", self._session("lease/release"), attempts=1)

    async def context(self) -> dict[str, Any]:
        ctx: dict[str, Any] = await self._request("GET", self._session("context"))
        return ctx

    async def submit_segment(self, submission: dict[str, Any]) -> dict[str, Any]:
        # Retries reuse the same segment_key, so they are idempotent on the server.
        res: dict[str, Any] = await self._request("POST", self._session("transcript-segments"), body=submission, attempts=6)
        return res

    async def prompt_started(self, activation_id: str, at_ms: int) -> dict[str, Any]:
        ctx: dict[str, Any] = await self._request(
            "POST", self._session("prompt-started"), body={"activation_id": activation_id, "at_ms": at_ms}
        )
        return ctx

    async def calibration_complete(self, state_version: int) -> dict[str, Any]:
        ctx: dict[str, Any] = await self._request("POST", self._session("calibration-complete"), body={"state_version": state_version})
        return ctx

    async def answer_complete(self, state_version: int, activation_id: str, keys: list[str], cause: str) -> dict[str, Any]:
        body = {"state_version": state_version, "activation_id": activation_id, "flushed_segment_keys": keys, "cause": cause}
        for _ in range(10):
            try:
                ctx: dict[str, Any] = await self._request("POST", self._session("answer-complete"), body=body)
                return ctx
            except ControlPlaneError as e:
                if e.code != "manifest_incomplete":
                    raise
                await asyncio.sleep(0.2)  # a flushed segment is still in flight
        raise ControlPlaneError(409, "manifest_incomplete")

    async def pause(self, state_version: int, reason: str) -> dict[str, Any]:
        ctx: dict[str, Any] = await self._request("POST", self._session("pause"), body={"state_version": state_version, "reason": reason})
        return ctx

    async def telemetry(self, metrics: list[dict[str, Any]], flags: list[dict[str, Any]]) -> None:
        await self._request("POST", self._session("telemetry"), body={"metrics": metrics, "policy_flags": flags}, attempts=2)

    async def usage(self, report: dict[str, Any]) -> None:
        await self._request("POST", self._session("usage"), body=report, attempts=2)
