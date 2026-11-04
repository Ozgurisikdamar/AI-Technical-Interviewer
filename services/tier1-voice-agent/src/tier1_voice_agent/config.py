"""Agent configuration with the refusal rules of ADR-011 and ADR-020."""

from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass, replace
from datetime import UTC, date, datetime
from typing import Literal
from urllib.parse import urlparse

from .models import Registry, check_model

ENVIRONMENTS = ("dev", "test", "staging", "production")
DEV_SECRET_PREFIX = "ati-dev-"  # noqa: S105  (marker for development placeholders, not a secret)
# The provider endpoint is pinned explicitly: without it the realtime client falls back to the
# OPENAI_BASE_URL environment variable, which would send the key and candidate audio elsewhere.
OPENAI_DEFAULT_BASE_URL = "https://api.openai.com/v1"


class ConfigError(Exception):
    """Configuration is invalid or unsafe; the agent refuses to start."""


@dataclass(frozen=True)
class Config:
    app_env: str
    livekit_url: str
    livekit_api_key: str
    livekit_api_secret: str
    agent_name: str
    control_plane_url: str
    service_token: str
    engine: Literal["scripted", "openai"]
    http_proxy: str | None
    openai_api_key: str | None
    openai_model: str
    openai_voice: str
    openai_transcription_model: str
    openai_base_url: str
    health_port: int
    max_sessions: int
    # Non-fatal notices for the operator (e.g. a deprecated model), logged at startup.
    warnings: tuple[str, ...] = ()

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
    """Load configuration; raise ConfigError listing every problem. today and models replace the
    current date and the packaged model registry (tests)."""
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

    engine = env.get("ATI_VOICE_ENGINE", "openai").strip()
    if engine not in ("scripted", "openai"):
        problems.append("ATI_VOICE_ENGINE must be scripted or openai")
    proxy = env.get("ATI_AGENT_HTTP_PROXY", "").strip() or None
    cfg = Config(
        app_env=app_env,
        livekit_url=req("ATI_LIVEKIT_URL"),
        livekit_api_key=req("ATI_LIVEKIT_API_KEY"),
        livekit_api_secret=req("ATI_LIVEKIT_API_SECRET"),
        agent_name=env.get("ATI_AGENT_NAME", "ati-tier1-interviewer").strip(),
        control_plane_url=req("ATI_CONTROL_PLANE_INTERNAL_URL").rstrip("/"),
        service_token=req("ATI_AGENT_SERVICE_TOKEN"),
        engine=engine if engine in ("scripted", "openai") else "openai",  # type: ignore[arg-type]
        http_proxy=proxy,
        openai_api_key=env.get("OPENAI_API_KEY", "").strip() or None,
        # No defaults: the deployment names its models, which the registry checks (ADR-021).
        openai_model=env.get("ATI_OPENAI_REALTIME_MODEL", "").strip(),
        openai_voice=env.get("ATI_OPENAI_VOICE", "marin").strip(),
        openai_transcription_model=env.get("ATI_OPENAI_TRANSCRIPTION_MODEL", "").strip(),
        openai_base_url=env.get("ATI_OPENAI_BASE_URL", OPENAI_DEFAULT_BASE_URL).strip().rstrip("/"),
        health_port=int(env.get("ATI_AGENT_HEALTH_PORT", "18081")),
        # Concurrent interviews per worker process: an uncalibrated placeholder until capacity tests (Phase 6).
        max_sessions=_int(env.get("ATI_AGENT_MAX_SESSIONS", "4"), problems, "ATI_AGENT_MAX_SESSIONS", 1, 64),
    )
    override = env.get("LIVEKIT_AGENT_NAME_OVERRIDE", "").strip()
    if override and override != cfg.agent_name:
        # livekit-agents would silently register under this name instead of ATI_AGENT_NAME.
        problems.append("LIVEKIT_AGENT_NAME_OVERRIDE differs from ATI_AGENT_NAME; refusing an ambiguous agent name")
    if cfg.engine == "openai":
        if not cfg.openai_api_key:
            problems.append("the openai engine needs OPENAI_API_KEY; refusing to start without provider credentials")
        _check_openai(cfg, env, today or datetime.now(UTC).date(), problems, warnings, models)
    if cfg.production:
        if cfg.engine == "scripted":
            problems.append("the scripted engine produces synthetic transcripts and is refused in production")
        if urlparse(cfg.control_plane_url).scheme != "https":
            problems.append("production requires an https control-plane URL (TLS between agent and control plane)")
        if urlparse(cfg.livekit_url).scheme != "wss":
            problems.append("production requires a wss:// LiveKit URL")
        for name, value in (("ATI_LIVEKIT_API_SECRET", cfg.livekit_api_secret), ("ATI_AGENT_SERVICE_TOKEN", cfg.service_token)):
            if value.startswith(DEV_SECRET_PREFIX):
                problems.append(f"{name} is a development placeholder")
    if problems:
        raise ConfigError("; ".join(problems))
    if warnings:
        cfg = replace(cfg, warnings=tuple(warnings))
    return cfg


def _check_openai(
    cfg: Config, env: Mapping[str, str], today: date, problems: list[str], warnings: list[str], models: Registry | None
) -> None:
    strict = cfg.app_env in ("staging", "production")
    for key, model, purpose in (
        ("ATI_OPENAI_REALTIME_MODEL", cfg.openai_model, "tier1_realtime"),
        ("ATI_OPENAI_TRANSCRIPTION_MODEL", cfg.openai_transcription_model, "tier1_transcription"),
    ):
        if not model:
            problems.append(f"{key} is required for the openai engine (see contracts/models/model-registry.v1.json)")
            continue
        check_model(
            key,
            model,
            provider="openai",
            purpose=purpose,
            today=today,
            strict=strict,
            production=cfg.production,
            problems=problems,
            warnings=warnings,
            models=models,
        )
    url = urlparse(cfg.openai_base_url)
    local_ok = url.scheme == "http" and cfg.app_env in ("dev", "test")
    if (url.scheme != "https" and not local_ok) or not url.hostname:
        problems.append("ATI_OPENAI_BASE_URL must be an https URL (http only for a local endpoint in dev and test)")
    elif cfg.production and cfg.openai_base_url != OPENAI_DEFAULT_BASE_URL:
        problems.append("production uses the provider endpoint pinned in ADR-021; another ATI_OPENAI_BASE_URL needs a new decision")
    inherited = env.get("OPENAI_BASE_URL", "").strip().rstrip("/")
    if inherited and inherited != cfg.openai_base_url:
        problems.append("OPENAI_BASE_URL differs from ATI_OPENAI_BASE_URL; refusing an ambiguous provider endpoint")
