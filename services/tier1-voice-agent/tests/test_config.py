"""Refusal rules (ADR-011, ADR-020): the agent never starts in an unsafe configuration."""

from __future__ import annotations

import pytest

from tier1_voice_agent.config import ConfigError, load

BASE = {
    "APP_ENV": "dev",
    "ATI_VOICE_ENGINE": "scripted",
    "ATI_LIVEKIT_URL": "ws://localhost:7880",
    "ATI_LIVEKIT_API_KEY": "devkey",
    "ATI_LIVEKIT_API_SECRET": "ati-dev-livekit-secret",
    "ATI_CONTROL_PLANE_INTERNAL_URL": "http://127.0.0.1:8081/",
    "ATI_AGENT_SERVICE_TOKEN": "ati-dev-agent-token",
}

PROD = {
    **BASE,
    "APP_ENV": "production",
    "ATI_VOICE_ENGINE": "openai",
    "OPENAI_API_KEY": "test-only-openai",
    "ATI_LIVEKIT_URL": "wss://media.example.invalid",
    "ATI_LIVEKIT_API_SECRET": "test-only-livekit-secret",
    "ATI_CONTROL_PLANE_INTERNAL_URL": "https://control.example.invalid",
    "ATI_AGENT_SERVICE_TOKEN": "test-only-agent-token",
}


def test_dev_scripted_loads() -> None:
    cfg = load(BASE)
    assert cfg.engine == "scripted"
    assert cfg.control_plane_url == "http://127.0.0.1:8081"  # trailing slash removed
    assert cfg.http_proxy is None  # proxies are never inherited


@pytest.mark.parametrize("value", ["", "prod", "Production", "local"])
def test_unknown_or_missing_app_env_is_refused(value: str) -> None:
    with pytest.raises(ConfigError, match="APP_ENV"):
        load({**BASE, "APP_ENV": value})


def test_missing_required_values_are_all_reported() -> None:
    with pytest.raises(ConfigError) as err:
        load({"APP_ENV": "dev", "ATI_VOICE_ENGINE": "scripted"})
    for key in (
        "ATI_LIVEKIT_URL",
        "ATI_LIVEKIT_API_KEY",
        "ATI_LIVEKIT_API_SECRET",
        "ATI_CONTROL_PLANE_INTERNAL_URL",
        "ATI_AGENT_SERVICE_TOKEN",
    ):
        assert key in str(err.value)


def test_openai_engine_without_key_is_refused() -> None:
    with pytest.raises(ConfigError, match="OPENAI_API_KEY"):
        load({**BASE, "ATI_VOICE_ENGINE": "openai"})


def test_default_engine_is_openai_and_needs_a_key() -> None:
    env = dict(BASE)
    del env["ATI_VOICE_ENGINE"]
    with pytest.raises(ConfigError, match="OPENAI_API_KEY"):
        load(env)


def test_unknown_engine_is_refused() -> None:
    with pytest.raises(ConfigError, match="ATI_VOICE_ENGINE"):
        load({**BASE, "ATI_VOICE_ENGINE": "other"})


def test_production_accepts_a_safe_configuration() -> None:
    assert load(PROD).production


@pytest.mark.parametrize(
    ("override", "message"),
    [
        ({"ATI_VOICE_ENGINE": "scripted"}, "scripted engine"),
        ({"ATI_CONTROL_PLANE_INTERNAL_URL": "http://control.example.invalid"}, "https control-plane"),
        ({"ATI_LIVEKIT_URL": "ws://media.example.invalid"}, "wss://"),
        ({"ATI_LIVEKIT_API_SECRET": "ati-dev-livekit-secret"}, "ATI_LIVEKIT_API_SECRET is a development placeholder"),
        ({"ATI_AGENT_SERVICE_TOKEN": "ati-dev-agent-token"}, "ATI_AGENT_SERVICE_TOKEN is a development placeholder"),
    ],
)
def test_production_refusals(override: dict[str, str], message: str) -> None:
    with pytest.raises(ConfigError, match=message):
        load({**PROD, **override})


@pytest.mark.parametrize("value", ["0", "65", "many"])
def test_max_sessions_is_bounded(value: str) -> None:
    with pytest.raises(ConfigError, match="ATI_AGENT_MAX_SESSIONS"):
        load({**BASE, "ATI_AGENT_MAX_SESSIONS": value})


def test_explicit_proxy_is_kept() -> None:
    assert load({**BASE, "ATI_AGENT_HTTP_PROXY": "http://proxy.invalid:3128"}).http_proxy == "http://proxy.invalid:3128"


def test_agent_name_override_must_not_redirect_registration() -> None:
    with pytest.raises(ConfigError, match="LIVEKIT_AGENT_NAME_OVERRIDE"):
        load({**BASE, "LIVEKIT_AGENT_NAME_OVERRIDE": "other-agent"})
    assert load({**BASE, "LIVEKIT_AGENT_NAME_OVERRIDE": "ati-tier1-interviewer"}).agent_name == "ati-tier1-interviewer"
