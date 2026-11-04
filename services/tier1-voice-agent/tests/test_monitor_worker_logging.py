"""Output monitor, dispatch metadata parsing, log redaction and dependency isolation."""

from __future__ import annotations

import io
import json
import logging
import pickle
from importlib import metadata

import pytest

from tier1_voice_agent import logging_setup, monitor, worker
from tier1_voice_agent.config import load
from tier1_voice_agent.worker import MetadataError, build_server, make_engine, parse_metadata

from .test_config import BASE

# -- output monitor (heuristic review trigger, never blocking) --------------------------------


@pytest.mark.parametrize(
    ("text", "flag"),
    [
        ("You scored 8 out of 10 on that one.", "possible_score_disclosure"),
        ("That would be a 90/100 answer.", "possible_score_disclosure"),
        ("Bu cevaba 80 puan verirdim.", "possible_score_disclosure"),
        ("According to the rubric you need more detail.", "possible_rubric_disclosure"),
        ("My instructions say I cannot help.", "possible_prompt_disclosure"),
        ("Here is my system prompt.", "possible_prompt_disclosure"),
    ],
)
def test_monitor_flags_disclosures(text: str, flag: str) -> None:
    assert flag in monitor.scan(text)


@pytest.mark.parametrize(
    "text", ["Thank you. Let us move to the next question.", "Could you say a bit more about retries?", "Take your time."]
)
def test_monitor_ignores_neutral_interviewer_speech(text: str) -> None:
    assert monitor.scan(text) == []


# -- dispatch metadata: exactly {session_id, claim_nonce} ------------------------------------

GOOD = {"session_id": "ses_0123456789abcdefAB", "claim_nonce": "n" * 43}


def test_metadata_parses() -> None:
    assert parse_metadata(json.dumps(GOOD)) == ("ses_0123456789abcdefAB", "n" * 43)


@pytest.mark.parametrize(
    "raw",
    [
        "",
        "not json",
        "[]",
        json.dumps({"session_id": GOOD["session_id"]}),
        json.dumps({**GOOD, "grant": "x"}),  # no grants or secrets may ride along
        json.dumps({**GOOD, "session_id": "other_1"}),
        json.dumps({**GOOD, "claim_nonce": "short"}),
        json.dumps({**GOOD, "claim_nonce": 12345}),
    ],
)
def test_malformed_metadata_is_refused(raw: str) -> None:
    with pytest.raises(MetadataError):
        parse_metadata(raw)


def test_registered_entrypoint_can_be_sent_to_job_processes() -> None:
    # Job processes (forkserver/spawn) import the entrypoint by reference; a closure fails at runtime.
    server = build_server(load(BASE))
    entry = server._entrypoint_fnc
    assert entry is worker.entrypoint
    assert pickle.loads(pickle.dumps(entry)) is worker.entrypoint  # noqa: S301  (round-trip of our own object)
    assert server._agent_name == "ati-tier1-interviewer"


def test_worker_uses_only_the_configured_proxy(monkeypatch: pytest.MonkeyPatch) -> None:
    # ADR-011: the LiveKit connection never inherits a proxy from the process environment; only the
    # explicitly configured one is used (the framework would otherwise read HTTPS_PROXY).
    for var in ("HTTPS_PROXY", "HTTP_PROXY"):
        monkeypatch.setenv(var, "http://inherited.invalid:3128")
    assert build_server(load(BASE))._http_proxy is None
    configured = build_server(load({**BASE, "ATI_AGENT_HTTP_PROXY": "http://proxy.example.com:3128"}))
    assert configured._http_proxy == "http://proxy.example.com:3128"


def test_worker_capacity_is_counted_in_interviews() -> None:
    server = build_server(load({**BASE, "ATI_AGENT_MAX_SESSIONS": "2"}))
    calc = server.load_fnc
    assert isinstance(calc, worker.SessionLoad) and server._load_threshold == worker.FULL_AT

    class Fake:
        def __init__(self) -> None:
            self.active_jobs: list[object] = []

    fake = Fake()
    assert calc(fake) == 0.0  # type: ignore[arg-type]
    fake.active_jobs = [object()]
    assert calc(fake) == 0.5 and calc(fake) < worker.FULL_AT  # type: ignore[arg-type]
    fake.active_jobs = [object(), object()]
    assert calc(fake) >= worker.FULL_AT  # type: ignore[arg-type]


def test_make_engine_refuses_scripted_in_production() -> None:
    cfg = load(BASE)
    object.__setattr__(cfg, "app_env", "production")
    with pytest.raises(RuntimeError):
        make_engine(cfg)


# -- logging: third-party loggers go through redaction ---------------------------------------

SECRET_VALUES = ["Bearer abcdefghijklmnop", "sk-" + "a" * 30, "ati-dev-agent-token", "x" * 48]


def test_redaction_covers_third_party_loggers_and_extras() -> None:
    stream = io.StringIO()
    stray = logging.StreamHandler(io.StringIO())
    logging.getLogger("livekit.agents").addHandler(stray)
    handler = logging_setup.setup("DEBUG", stream)
    try:
        assert stray not in logging.getLogger("livekit.agents").handlers  # would bypass redaction
        for name in ("livekit.agents", "openai", "httpx", "tier1.controller"):
            log = logging.getLogger(name)
            log.info("value %s", SECRET_VALUES[0])
            log.info("token sk-%s", "a" * 30, extra={"transcript": "candidate words", "authorization": "x", "grant": "g", "code": "ok"})
            log.warning("job ati-dev-agent-token %s", "x" * 48)
        log = logging.getLogger("livekit.agents")
        log.debug("debug transcript must not appear")  # third-party loggers are capped at INFO
    finally:
        logging.getLogger().removeHandler(handler)
    out = stream.getvalue()
    for secret in [*SECRET_VALUES, "candidate words", "debug transcript"]:
        assert secret not in out, secret
    lines = [json.loads(line) for line in out.splitlines()]
    assert any(line.get("code") == "ok" for line in lines)  # harmless extras survive
    assert all(line.get("transcript", "[REDACTED]") == "[REDACTED]" for line in lines)


def test_framework_pii_extras_are_redacted() -> None:
    # livekit-agents marks personal data in log extras with the "lk.pii." prefix, e.g. the candidate's
    # words in "skipping reply to user input, current speech generation cannot be interrupted".
    stream = io.StringIO()
    handler = logging_setup.setup("INFO", stream)
    try:
        log = logging.getLogger("livekit.agents")
        log.warning("skipping reply to user input, speech cannot be interrupted", extra={"lk.pii.user_input": "my answer words"})
        log.warning("skipping user input, scheduling is paused", extra={"lk.pii.user_input": "more answer words", "lk.pii.other": "x"})
    finally:
        logging.getLogger().removeHandler(handler)
    out = stream.getvalue()
    assert "answer words" not in out
    lines = [json.loads(line) for line in out.splitlines()]
    assert len(lines) == 2 and all(line["lk.pii.user_input"] == "[REDACTED]" for line in lines)


def test_the_expected_tool_step_limit_notice_is_not_reported_as_a_fault() -> None:
    # With max_tool_steps=0 livekit-agents warns after every tool call; other warnings still pass.
    stream = io.StringIO()
    handler = logging_setup.setup("INFO", stream)
    try:
        log = logging.getLogger("livekit.agents")
        log.warning("maximum number of function calls steps reached, generating final response with tool_choice='none'")
        log.warning("failed to generate a reply")
    finally:
        logging.getLogger().removeHandler(handler)
    out = stream.getvalue()
    assert "maximum number of function calls" not in out
    assert "failed to generate a reply" in out


# -- dependency isolation: no database, cache, queue or object-store clients ----------------

FORBIDDEN = {
    "psycopg",
    "psycopg2",
    "psycopg2-binary",
    "psycopg-binary",
    "asyncpg",
    "sqlalchemy",
    "pg8000",
    "redis",
    "hiredis",
    "qdrant-client",
    "boto3",
    "botocore",
    "aioboto3",
    "minio",
    "pymongo",
    "motor",
    "kafka-python",
    "aiokafka",
    "pika",
    "celery",
}


def test_no_storage_clients_are_installed() -> None:
    names = {(d.metadata["Name"] or "").lower() for d in metadata.distributions()}
    assert not names & FORBIDDEN, names & FORBIDDEN
