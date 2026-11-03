"""Agent configuration with the refusal rules of ADR-011 and ADR-020."""

from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass
from typing import Literal
from urllib.parse import urlparse

ENVIRONMENTS = ("dev", "test", "staging", "production")
DEV_SECRET_PREFIX = "ati-dev-"  # noqa: S105  (marker for development placeholders, not a secret)


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
    health_port: int

    @property
    def production(self) -> bool:
        return self.app_env == "production"


def load(env: Mapping[str, str]) -> Config:
    """Load configuration; raise ConfigError listing every problem."""
    problems: list[str] = []

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
        openai_model=env.get("ATI_OPENAI_REALTIME_MODEL", "gpt-realtime").strip(),
        openai_voice=env.get("ATI_OPENAI_VOICE", "marin").strip(),
        openai_transcription_model=env.get("ATI_OPENAI_TRANSCRIPTION_MODEL", "gpt-4o-transcribe").strip(),
        health_port=int(env.get("ATI_AGENT_HEALTH_PORT", "18081")),
    )
    if cfg.engine == "openai" and not cfg.openai_api_key:
        problems.append("the openai engine needs OPENAI_API_KEY; refusing to start without provider credentials")
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
    return cfg
