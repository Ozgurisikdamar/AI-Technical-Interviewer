"""Redaction of logs (identifiers and outcomes only)."""

from __future__ import annotations

import io
import json
import logging

from tier2_evaluator import logging_setup


def capture() -> tuple[io.StringIO, logging.Logger]:
    stream = io.StringIO()
    logging_setup.setup("DEBUG", stream)
    return stream, logging.getLogger("tier2.test")


def lines(stream: io.StringIO) -> list[dict[str, object]]:
    return [json.loads(x) for x in stream.getvalue().splitlines()]


def test_credentials_and_connection_secrets_are_scrubbed() -> None:
    stream, log = capture()
    log.warning("calling with Bearer abc.def and sk-0123456789abcdefABCDEF")
    log.warning("db postgres://ati_tier2_evaluator:s3cret@db.example.invalid:5432/ati and rediss://default:pw@redis:6380")
    log.warning("token ati-dev-tier2-service-token")
    text = stream.getvalue()
    for secret in ("abc.def", "sk-0123456789", "s3cret", ":pw@", "ati-dev-tier2"):
        assert secret not in text


def test_sensitive_extra_fields_are_redacted_and_identifiers_kept() -> None:
    stream, log = capture()
    log.info(
        "attempt recorded", extra={"session_id": "ses_0123456789abcdefAB", "quote": "candidate words", "output": {"x": 1}, "prompt": "p"}
    )
    (line,) = lines(stream)
    assert line["session_id"] == "ses_0123456789abcdefAB"
    assert line["quote"] == line["output"] == line["prompt"] == logging_setup.REDACTED


def test_third_party_debug_output_is_capped() -> None:
    stream, _ = capture()
    logging.getLogger("anthropic").debug("request body with candidate words")
    logging.getLogger("openai").debug("request body with candidate words")
    logging.getLogger("httpx").info("HTTP Request: POST https://api.openai.com/v1/responses")
    assert stream.getvalue() == ""


def test_level_names_are_case_insensitive_and_unknown_levels_fall_back_to_info() -> None:
    logging_setup.setup("info", io.StringIO())
    assert logging.getLogger().level == logging.INFO
    logging_setup.setup("debug", io.StringIO())
    assert logging.getLogger().level == logging.DEBUG
    logging_setup.setup("verbose", io.StringIO())
    assert logging.getLogger().level == logging.INFO
