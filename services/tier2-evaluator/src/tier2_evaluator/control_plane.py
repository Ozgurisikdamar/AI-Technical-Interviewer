"""Client for the control plane's internal Tier-2 API (ADR-015, ADR-022).

The worker writes nothing to the database: every attempt and the end of an interview's evaluation
are reported here, with the Tier-2 service credential. Proxies are never inherited from the
environment: the internal listener is not reached through an egress proxy.
"""

from __future__ import annotations

import asyncio
from typing import Any

import httpx

_RETRYABLE = {502, 503, 504}


class ControlPlaneError(Exception):
    def __init__(self, status: int, code: str, detail: str = "") -> None:
        super().__init__(f"{status} {code}")
        self.status, self.code, self.detail = status, code, detail

    @property
    def transient(self) -> bool:
        """Worth retrying the delivery later: unreachable, overloaded or failing."""
        return self.status == 0 or self.status == 429 or self.status >= 500


class ControlPlane:
    def __init__(
        self, base_url: str, service_token: str, *, timeout: float = 10.0, transport: httpx.AsyncBaseTransport | None = None
    ) -> None:
        self._client = httpx.AsyncClient(base_url=base_url, timeout=timeout, trust_env=False, transport=transport)
        self._headers = {"Authorization": f"Bearer {service_token}"}

    async def aclose(self) -> None:
        await self._client.aclose()

    async def record_evaluation(self, session_id: str, submission: dict[str, Any]) -> dict[str, Any]:
        out: dict[str, Any] = await self._post(f"/internal/v1/tier2/sessions/{session_id}/evaluations", submission)
        return out

    async def complete(self, session_id: str, failure_reason: str | None = None) -> dict[str, Any]:
        out: dict[str, Any] = await self._post(f"/internal/v1/tier2/sessions/{session_id}/complete", {"failure_reason": failure_reason})
        return out

    async def _post(self, path: str, body: Any, attempts: int = 4) -> Any:
        delay = 0.2
        for attempt in range(attempts):
            last = attempt == attempts - 1
            try:
                resp = await self._client.post(path, json=body, headers=self._headers)
            except httpx.TransportError:
                if last:
                    raise ControlPlaneError(0, "unreachable") from None
            else:
                if resp.status_code < 400:
                    return resp.json()
                code, detail = "error", ""
                try:
                    problem = resp.json()
                    code, detail = str(problem.get("code", "error")), str(problem.get("detail", ""))
                except ValueError:
                    pass
                if resp.status_code not in _RETRYABLE or last:
                    raise ControlPlaneError(resp.status_code, code, detail)
            await asyncio.sleep(delay)
            delay *= 2
        raise ControlPlaneError(0, "unreachable")  # pragma: no cover  (the loop always returns or raises)
