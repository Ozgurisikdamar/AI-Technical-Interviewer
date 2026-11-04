"""Worker configuration with refusal rules (ADR-020, ADR-021, ADR-022)."""

from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass, field
from datetime import UTC, date, datetime
from typing import Literal
from urllib.parse import urlparse

from .models import Registry, check_model

ENVIRONMENTS = ("dev", "test", "staging", "production")
EVALUATORS = ("reference", "openai", "anthropic")
DEV_SECRET_PREFIX = "ati-dev-"  # noqa: S105  (marker for development placeholders, not a secret)
OPENAI_DEFAULT_BASE_URL = "https://api.openai.com/v1"
ANTHROPIC_DEFAULT_BASE_URL = "https://api.anthropic.com"


class ConfigError(Exception):
    """Configuration is invalid or unsafe; the worker refuses to start."""


@dataclass(frozen=True)
class Config:
    app_env: str
    redis_url: str
    database_url: str
    control_plane_url: str
    service_token: str
    evaluator: Literal["reference", "openai", "anthropic"]
    model_id: str | None
    api_key: str | None
    base_url: str | None
    consumer_name: str
    max_deliveries: int
    visibility_timeout_ms: int
    retention_note: str = "raw evaluator output is kept by the control plane under the session's assessment-output retention"
    warnings: tuple[str, ...] = field(default=())

    @property
    def production(self) -> bool:
        return self.app_env == "production"


def _int(raw: str, problems: list[str], key: str, low: int, high: int) -> int:
    try:
        value = int(raw)
    except ValueError:
        value = -1
    if not low <= value <= high:
        problems.append(f"{key} must be an integer between {low} and {high}")
        return low
    return value


def load(env: Mapping[str, str], *, today: date | None = None, models: Registry | None = None) -> Config:
    """Load configuration; raise ConfigError listing every problem."""
    problems: list[str] = []
    warnings: list[str] = []

    def req(key: str) -> str:
        value = env.get(key, "").strip()
        if not value:
            problems.append(f"{key} is required")
        return value

    app_env = env.get("APP_ENV", "").strip()
    if app_env not in ENVIRONMENTS:
        raise ConfigError(f"APP_ENV must be one of {', '.join(ENVIRONMENTS)} (got {app_env!r}); refusing to start")
    evaluator = env.get("ATI_TIER2_EVALUATOR", "").strip()
    if evaluator not in EVALUATORS:
        problems.append("ATI_TIER2_EVALUATOR must be reference, openai or anthropic")
        evaluator = "reference"
    production = app_env == "production"

    model_id: str | None = None
    api_key: str | None = None
    base_url: str | None = None
    if evaluator == "reference":
        if production:
            problems.append("the reference evaluator scores synthetic rubrics by keyword and is refused in production")
    else:
        model_id = req("ATI_TIER2_MODEL")
        key_name = "OPENAI_API_KEY" if evaluator == "openai" else "ANTHROPIC_API_KEY"
        api_key = env.get(key_name, "").strip() or None
        if not api_key:
            problems.append(f"the {evaluator} evaluator needs {key_name}")
        default_url = OPENAI_DEFAULT_BASE_URL if evaluator == "openai" else ANTHROPIC_DEFAULT_BASE_URL
        base_url = env.get("ATI_TIER2_BASE_URL", default_url).strip().rstrip("/")
        url = urlparse(base_url)
        if url.scheme != "https" or not url.hostname:
            problems.append("ATI_TIER2_BASE_URL must be an https URL")
        elif production and base_url != default_url:
            problems.append("production uses the provider endpoint pinned in ADR-021; another ATI_TIER2_BASE_URL needs a new decision")
        inherited_name = "OPENAI_BASE_URL" if evaluator == "openai" else "ANTHROPIC_BASE_URL"
        inherited = env.get(inherited_name, "").strip().rstrip("/")
        if inherited and inherited != base_url:
            problems.append(f"{inherited_name} differs from ATI_TIER2_BASE_URL; refusing an ambiguous provider endpoint")
        if evaluator == "anthropic" and env.get("ANTHROPIC_CUSTOM_HEADERS", "").strip():
            # Extra headers could switch on beta features such as server-side fallbacks, which would
            # change the model that produced an evaluation (ADR-022).
            problems.append("ANTHROPIC_CUSTOM_HEADERS is not supported; the evaluator sends only its own headers")
        if model_id:
            check_model(
                model_id,
                provider=evaluator,
                today=today or datetime.now(UTC).date(),
                strict=app_env in ("staging", "production"),
                production=production,
                problems=problems,
                warnings=warnings,
                models=models,
            )

    cfg = Config(
        app_env=app_env,
        redis_url=req("ATI_REDIS_URL"),
        database_url=req("ATI_TIER2_DATABASE_URL"),
        control_plane_url=req("ATI_CONTROL_PLANE_INTERNAL_URL").rstrip("/"),
        service_token=req("ATI_TIER2_SERVICE_TOKEN"),
        evaluator=evaluator,  # type: ignore[arg-type]
        model_id=model_id,
        api_key=api_key,
        base_url=base_url,
        consumer_name=env.get("ATI_TIER2_CONSUMER", "tier2-1").strip() or "tier2-1",
        max_deliveries=_int(env.get("ATI_TIER2_MAX_DELIVERIES", "5"), problems, "ATI_TIER2_MAX_DELIVERIES", 1, 50),
        visibility_timeout_ms=_int(
            env.get("ATI_TIER2_VISIBILITY_TIMEOUT_MS", "120000"), problems, "ATI_TIER2_VISIBILITY_TIMEOUT_MS", 1000, 3_600_000
        ),
    )
    if production:
        if urlparse(cfg.control_plane_url).scheme != "https":
            problems.append("production requires an https control-plane URL")
        if not cfg.redis_url.startswith("rediss://"):
            problems.append("production requires TLS to Redis (rediss://)")
        if "sslmode=verify-full" not in cfg.database_url:
            problems.append("production requires a verified TLS connection to PostgreSQL (sslmode=verify-full)")
        if cfg.service_token.startswith(DEV_SECRET_PREFIX):
            problems.append("ATI_TIER2_SERVICE_TOKEN is a development placeholder")
    if problems:
        raise ConfigError("; ".join(problems))
    if warnings:
        cfg = Config(**{**cfg.__dict__, "warnings": tuple(warnings)})
    return cfg
