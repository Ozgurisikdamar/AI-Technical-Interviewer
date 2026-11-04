"""Logging with mandatory redaction (MVP criterion 10).

The agent never logs transcripts, prompts, grants or credentials on purpose. This filter is a
second line of defence for third-party loggers (livekit, openai, httpx), whose debug output can
contain transcripts or headers.
"""

from __future__ import annotations

import json
import logging
import re
import sys
import time
from typing import Any

REDACTED = "[REDACTED]"
# "pii" covers livekit-agents' own marker for personal data in log extras ("lk.pii.user_input" carries the
# candidate's words in warnings such as "skipping reply to user input").
_SENSITIVE_KEY = re.compile(
    r"(authorization|cookie|token|secret|password|grant|nonce|api[_-]?key|credential|transcript|text|quote|instructions|audio|email|content|pii)",
    re.I,
)
_SENSITIVE_VALUE = re.compile(
    r"(Bearer\s+\S+|eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]+|sk-[A-Za-z0-9_-]{16,}|ati-dev-[A-Za-z0-9_-]+|[A-Za-z0-9_-]{40,})"
)
# Third-party loggers that may emit transcripts at debug level are capped at INFO.
_NOISY = ("livekit", "livekit.agents", "livekit.plugins", "openai", "httpx", "httpcore", "websockets", "aiohttp")
# Per-request lines (every context poll, every segment) are noise and carry URLs; keep warnings only.
_QUIET = ("httpx", "httpcore", "aiohttp.access")


class ExpectedToolStepLimit(logging.Filter):
    """With max_tool_steps=0 (no chained tool calls, ADR-011) livekit-agents warns after every tool
    call that the step limit was reached. That is the policy working, not a fault: drop it."""

    def filter(self, record: logging.LogRecord) -> bool:
        return not (record.levelno == logging.WARNING and str(record.msg).startswith("maximum number of function calls steps reached"))


def scrub(value: str) -> str:
    return _SENSITIVE_VALUE.sub(REDACTED, value)


class RedactingFilter(logging.Filter):
    def filter(self, record: logging.LogRecord) -> bool:
        if isinstance(record.args, dict):
            record.args = {k: (REDACTED if _SENSITIVE_KEY.search(str(k)) else v) for k, v in record.args.items()}
        try:
            rendered = record.getMessage()
        except Exception:  # a malformed third-party format string must not leak its arguments
            rendered = str(record.msg)
        # Scrub the rendered message: a secret may be split between the format string and its args.
        record.msg, record.args = scrub(rendered), None
        for key in list(record.__dict__):
            if key not in _STANDARD and _SENSITIVE_KEY.search(key):
                record.__dict__[key] = REDACTED
        return True


def _scrub_any(value: Any) -> Any:
    if isinstance(value, str):
        return scrub(value)
    return value


_STANDARD = set(logging.LogRecord("x", 0, "x", 0, "x", None, None).__dict__) | {"message", "asctime"}


class JsonFormatter(logging.Formatter):
    converter = time.gmtime  # type: ignore[assignment]  # UTC timestamps (documented logging idiom)

    def format(self, record: logging.LogRecord) -> str:
        out: dict[str, Any] = {
            "time": self.formatTime(record, "%Y-%m-%dT%H:%M:%S") + f".{int(record.msecs):03d}Z",
            "level": record.levelname,
            "logger": record.name,
            "msg": record.getMessage(),
        }
        for key, value in record.__dict__.items():
            if key not in _STANDARD and not key.startswith("_"):
                out[key] = REDACTED if _SENSITIVE_KEY.search(key) else _scrub_any(value) if isinstance(value, str) else value
        if record.exc_info:
            out["exc"] = scrub(self.formatException(record.exc_info))
        return json.dumps(out, default=str)


def setup(level: str = "INFO", stream: Any = None) -> logging.Handler:
    handler = logging.StreamHandler(stream or sys.stdout)
    handler.addFilter(ExpectedToolStepLimit())
    handler.addFilter(RedactingFilter())
    handler.setFormatter(JsonFormatter())
    root = logging.getLogger()
    for h in list(root.handlers):
        root.removeHandler(h)
    root.addHandler(handler)
    root.setLevel(level)
    for name in _NOISY:
        logger = logging.getLogger(name)
        logger.setLevel(max(logging.INFO, logging.getLevelName(level) if isinstance(logging.getLevelName(level), int) else logging.INFO))
        logger.propagate = True
        for h in list(logger.handlers):  # third-party handlers would bypass redaction
            logger.removeHandler(h)
    for name in _QUIET:
        logging.getLogger(name).setLevel(logging.WARNING)
    return handler
