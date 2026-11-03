"""Output monitor, dispatch metadata parsing, log redaction and dependency isolation."""

from __future__ import annotations

import io
import json
import logging
from importlib import metadata

import pytest

from tier1_voice_agent import logging_setup, monitor
from tier1_voice_agent.config import load
from tier1_voice_agent.worker import MetadataError, make_engine, parse_metadata

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
