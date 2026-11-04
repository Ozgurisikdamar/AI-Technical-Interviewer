"""Logging with mandatory redaction.

The worker logs identifiers, outcomes and timings. It never logs transcripts, quotes, prompts,
evaluator output or credentials on purpose; this filter is a second line of defence for
third-party loggers (openai, anthropic, httpx, psycopg), whose debug output can contain request
bodies and headers.
"""

from __future__ import annotations

import json
import logging
import re
import sys
import time
from typing import Any

REDACTED = "[REDACTED]"
_SENSITIVE_KEY = re.compile(
    r"(authorization|cookie|token|secret|password|api[_-]?key|credential|transcript|text|quote|prompt|output|content|body|email|pii)",
    re.I,
)
_SENSITIVE_VALUE = re.compile(
    r"(Bearer\s+\S+|eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]+|sk-[A-Za-z0-9_-]{16,}|ati-dev-[A-Za-z0-9_-]+|"
    r"(?:postgres(?:ql)?|rediss?)://[^\s@/]*:[^\s@/]*@|[A-Za-z0-9_-]{40,})"
)
# Third-party loggers that may emit request bodies at debug level are capped at INFO.
_NOISY = ("openai", "anthropic", "httpx", "httpx2", "httpcore", "psycopg", "redis")
# Per-request lines carry URLs and are noise; keep warnings only.
_QUIET = ("httpx", "httpx2", "httpcore")


def scrub(value: str) -> str:
    return _SENSITIVE_VALUE.sub(REDACTED, value)


_STANDARD = set(logging.LogRecord("x", 0, "x", 0, "x", None, None).__dict__) | {"message", "asctime"}


class RedactingFilter(logging.Filter):
    def filter(self, record: logging.LogRecord) -> bool:
        if isinstance(record.args, dict):
            record.args = {k: (REDACTED if _SENSITIVE_KEY.search(str(k)) else v) for k, v in record.args.items()}
        try:
            rendered = record.getMessage()
        except Exception:  # a malformed third-party format string must not leak its arguments
            rendered = str(record.msg)
        record.msg, record.args = scrub(rendered), None
        for key in list(record.__dict__):
            if key not in _STANDARD and _SENSITIVE_KEY.search(key):
                record.__dict__[key] = REDACTED
        return True


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
                out[key] = REDACTED if _SENSITIVE_KEY.search(key) else scrub(value) if isinstance(value, str) else value
        if record.exc_info:
            out["exc"] = scrub(self.formatException(record.exc_info))
        return json.dumps(out, default=str)


def setup(level: str = "INFO", stream: Any = None) -> logging.Handler:
    level = level.strip().upper()
    if not isinstance(logging.getLevelName(level), int):
        level = "INFO"  # an unknown level must not stop the worker from starting
    handler = logging.StreamHandler(stream or sys.stdout)
    handler.addFilter(RedactingFilter())
    handler.setFormatter(JsonFormatter())
    root = logging.getLogger()
    for h in list(root.handlers):
        root.removeHandler(h)
    root.addHandler(handler)
    root.setLevel(level)
    numeric = logging.getLevelName(level)
    for name in _NOISY:
        logger = logging.getLogger(name)
        logger.setLevel(max(logging.INFO, numeric if isinstance(numeric, int) else logging.INFO))
        logger.propagate = True
        for h in list(logger.handlers):  # third-party handlers would bypass redaction
            logger.removeHandler(h)
    for name in _QUIET:
        logging.getLogger(name).setLevel(logging.WARNING)
    return handler
