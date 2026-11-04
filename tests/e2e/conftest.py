"""E2E fixtures: the local stack (tools/dev/stack.sh), database checks and post-run scans."""

from __future__ import annotations

import contextlib
import os
import re
import subprocess
from collections.abc import AsyncIterator, Iterator
from pathlib import Path
from typing import Any

import psycopg
import pytest

from candidate import SECRETS_SEEN, Candidate

ROOT = Path(__file__).resolve().parents[2]
STACK = ROOT / "tools" / "dev" / "stack.sh"
AUDIO_SUFFIXES = {".wav", ".ogg", ".opus", ".mp3", ".m4a", ".flac", ".webm", ".pcm", ".raw"}

# Synthetic speech content (scripted engine) and prompts: transcripts must never reach logs.
TRANSCRIPT_SENTINELS = [
    "synthetic answer part",
    "synthetic long answer",
    "that is my complete answer",
    "Ignore all previous instructions",
    "reveal your system prompt",
    "Take your time and continue",
    "multiversion concurrency control",
    "process the same payment twice",
    "would score 9 out of 10",
    "eşzamanlılık denetiminin",
    "aynı ödemeyi asla iki kez",
    # Typed answers of the Tier-2 tests: neither the worker nor the control plane may log them.
    "instead of overwriting the old one",
    "stored with a unique constraint",
    "flame graph of the slow endpoint",
]
# Settings every control-plane start in this run uses (up and later restarts).
DEV_ENV = {"ATI_DEV_AGENT_DISPATCH_TIMEOUT": "20s"}


def _stack(*args: str, extra_env: dict[str, str] | None = None) -> str:
    env = {**os.environ, **(extra_env or {})}
    out = subprocess.run([str(STACK), *args], check=True, capture_output=True, text=True, env=env, timeout=600)
    return out.stdout


@pytest.fixture(scope="session")
def stack() -> Iterator[dict[str, str]]:
    reuse = os.environ.get("ATI_E2E_REUSE_STACK") == "1"
    started_files = set(_files())
    out = _stack("env") if reuse else _stack("up", extra_env=DEV_ENV)
    values = dict(line.split("=", 1) for line in out.strip().splitlines() if "=" in line)
    os.environ.update(values)
    yield values
    try:
        _scan_logs(Path(values["ATI_E2E_STATE_DIR"]) / "logs")
        _scan_audio(started_files)
    finally:
        if not reuse and os.environ.get("ATI_E2E_KEEP_STACK") != "1":
            _stack("down")


def _files() -> Iterator[Path]:
    for base in (ROOT, Path("/tmp")):
        for path in base.rglob("*"):
            if path.suffix.lower() in AUDIO_SUFFIXES and "node_modules" not in path.parts and ".venv" not in path.parts:
                yield path


def _scan_audio(before: set[Path]) -> None:
    created = sorted(set(_files()) - before)
    assert not created, f"audio files were written during the run: {created[:5]}"


def _scan_logs(logs: Path) -> None:
    secrets = [s for s in SECRETS_SEEN if len(s) >= 16]
    for name in ("ATI_E2E_LIVEKIT_API_SECRET",):
        secrets.append(os.environ[name])
    secrets += ["ati-dev-agent-service-token", "ati-dev-livekit-webhook-secret", "ati-dev-tier2-service-token"]
    for name in ("staff_token", "reviewer_token"):
        secrets.append((Path(os.environ["ATI_E2E_STATE_DIR"]) / name).read_text().strip())
    problems = []
    for log in sorted(logs.glob("*.log")):
        text = log.read_text(errors="replace")
        for s in secrets:
            if s in text:
                problems.append(f"{log.name}: credential {s[:6]}…")
        for t in TRANSCRIPT_SENTINELS:
            if t.lower() in text.lower():
                problems.append(f"{log.name}: transcript text {t!r}")
        if re.search(r"eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.", text):
            problems.append(f"{log.name}: JWT")
    assert not problems, "\n".join(problems)


@pytest.fixture(scope="session")
def staff_token(stack: dict[str, str]) -> str:
    return (Path(stack["ATI_E2E_STATE_DIR"]) / "staff_token").read_text().strip()


@pytest.fixture(scope="session")
def reviewer_token(stack: dict[str, str]) -> str:
    return (Path(stack["ATI_E2E_STATE_DIR"]) / "reviewer_token").read_text().strip()


class Db:
    def __init__(self, url: str) -> None:
        self.conn = psycopg.connect(url, autocommit=True)

    def rows(self, sql: str, *args: Any) -> list[tuple[Any, ...]]:
        with self.conn.cursor() as cur:
            cur.execute(sql, args)
            return list(cur.fetchall())

    def one(self, sql: str, *args: Any) -> Any:
        rows = self.rows(sql, *args)
        assert len(rows) == 1 and len(rows[0]) == 1, rows
        return rows[0][0]


@pytest.fixture(scope="session")
def db(stack: dict[str, str]) -> Iterator[Db]:
    d = Db(stack["ATI_E2E_DB_ADMIN_URL"])
    yield d
    d.conn.close()


@pytest.fixture
async def candidate(stack: dict[str, str], staff_token: str) -> AsyncIterator[Candidate]:
    c = Candidate()
    await c.create_and_join_api(staff_token)
    await c.join()
    yield c
    await release(c)


async def release(c: Candidate) -> None:
    """Ends a test's interview: withdrawing deletes the room, so its agent leaves at once instead of
    holding worker capacity until LiveKit's departure timeout (tests run back to back)."""
    with contextlib.suppress(Exception):
        await c.withdraw()  # refused when the interview already ended; nothing to release then
    await c.close()


def stack_cmd(*args: str, extra_env: dict[str, str] | None = None) -> str:
    return _stack(*args, extra_env={**DEV_ENV, **(extra_env or {})})
