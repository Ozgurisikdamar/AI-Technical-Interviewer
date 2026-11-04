"""Shared helpers for Tier-2 worker tests. No model provider and no control plane are contacted;
consumer tests use a throwaway local redis-server."""

from __future__ import annotations

import json
import shutil
import socket
import subprocess
import time
from collections.abc import AsyncIterator, Iterator
from pathlib import Path
from typing import Any

import pytest
import redis.asyncio as aioredis

from tier2_evaluator.evidence import AnswerEvidence, Segment, SessionEvidence, StoredRubric

REPO = Path(__file__).resolve().parents[3]
CONTRACTS = REPO / "contracts"
SESSION = "ses_0123456789abcdefAB"
TENANT = "ten_0123456789abcdefAB"


def rubric(name: str = "rub_demo_postgres_mvcc", version: int = 1, *, synthetic: bool | None = None) -> StoredRubric:
    body: dict[str, Any] = json.loads((CONTRACTS / "content" / "rubrics" / f"{name}.v{version}.json").read_text(encoding="utf-8"))
    return StoredRubric(body["rubric_id"], body["version"], body["synthetic"] if synthetic is None else synthetic, body)


def seg(n: int, text: str, *, start: int | None = None, end: int | None = None, confidence: float | None = None) -> Segment:
    s = 1000 * n if start is None else start
    return Segment(f"seg_{n:016d}AB", "candidate", s, s + 900 if end is None else end, text, confidence)


def answer(
    *texts: str,
    item: str = "q1",
    answer_id: str = "ans_0123456789abcdef01",
    rubric_id: str | None = "rub_demo_postgres_mvcc",
    rubric_version: int | None = 1,
    flags: tuple[str, ...] = (),
    missing: bool = False,
    transcript_version: int = 1,
    segments: tuple[Segment, ...] | None = None,
) -> AnswerEvidence:
    segs = segments if segments is not None else tuple(seg(i + 1, t) for i, t in enumerate(texts))
    return AnswerEvidence(
        SESSION, answer_id, item, transcript_version, "How does PostgreSQL MVCC work?", rubric_id, rubric_version, segs, missing, flags
    )


def session(
    *answers: AnswerEvidence,
    state: str = "EVALUATION_PENDING",
    language: str = "en",
    evaluated: frozenset[tuple[str, int, str, int, str]] = frozenset(),
) -> SessionEvidence:
    rubrics = {}
    for a in answers:
        if a.rubric_id is not None and a.rubric_version is not None:
            r = rubric(a.rubric_id, a.rubric_version)
            rubrics[(r.rubric_id, r.version)] = r
    return SessionEvidence(SESSION, TENANT, state, language, tuple(answers), rubrics, evaluated)


def completed_fields(answer_ids: list[str], *, session_id: str = SESSION) -> dict[str, str]:
    payload = {
        "event_type": "interview.completed",
        "event_version": 1,
        "event_id": "evt_0123456789abcdefAB",
        "occurred_at": "2026-11-04T10:00:00Z",
        "tenant_id": TENANT,
        "session_id": session_id,
        "plan": {"plan_id": "plan_demo_backend_en", "version": 1},
        "answer_ids": answer_ids,
    }
    return {
        "event_id": payload["event_id"],
        "event_type": "interview.completed",
        "session_id": session_id,
        "tenant_id": TENANT,
        "payload": json.dumps(payload),
    }


def _free_port() -> int:
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        port: int = s.getsockname()[1]
        return port


@pytest.fixture(scope="session")
def redis_server(tmp_path_factory: pytest.TempPathFactory) -> Iterator[str]:
    binary = shutil.which("redis-server")
    if binary is None:
        pytest.skip("redis-server is not installed")
    port = _free_port()
    workdir = tmp_path_factory.mktemp("redis")
    proc = subprocess.Popen(  # noqa: S603  (fixed local binary and arguments)
        [binary, "--port", str(port), "--bind", "127.0.0.1", "--save", "", "--appendonly", "no", "--dir", str(workdir)],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    deadline = time.monotonic() + 10
    while True:
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=0.2):
                break
        except OSError:
            if time.monotonic() > deadline or proc.poll() is not None:
                proc.kill()
                pytest.fail("redis-server did not start")
            time.sleep(0.05)
    yield f"redis://127.0.0.1:{port}/0"
    proc.terminate()
    proc.wait(timeout=10)


@pytest.fixture
async def redis_client(redis_server: str) -> AsyncIterator[aioredis.Redis]:
    client = aioredis.from_url(redis_server)
    await client.flushdb()
    yield client
    await client.aclose()
