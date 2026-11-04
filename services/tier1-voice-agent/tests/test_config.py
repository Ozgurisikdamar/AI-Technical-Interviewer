"""Refusal rules (ADR-011, ADR-020): the agent never starts in an unsafe configuration."""

from __future__ import annotations

from datetime import date

import pytest

from tier1_voice_agent.config import ConfigError, load
from tier1_voice_agent.models import parse, registry

BASE = {
    "APP_ENV": "dev",
    "ATI_VOICE_ENGINE": "scripted",
    "ATI_LIVEKIT_URL": "ws://localhost:7880",
    "ATI_LIVEKIT_API_KEY": "devkey",
    "ATI_LIVEKIT_API_SECRET": "ati-dev-livekit-secret",
    "ATI_CONTROL_PLANE_INTERNAL_URL": "http://127.0.0.1:8081/",
    "ATI_AGENT_SERVICE_TOKEN": "ati-dev-agent-token",
}

OPENAI = {
    **BASE,
    "ATI_VOICE_ENGINE": "openai",
    "OPENAI_API_KEY": "test-only-openai",
    "ATI_OPENAI_REALTIME_MODEL": "gpt-realtime-2.1",
    "ATI_OPENAI_TRANSCRIPTION_MODEL": "gpt-transcribe",
}

PROD = {
    **BASE,
    "APP_ENV": "production",
    "ATI_VOICE_ENGINE": "openai",
    "OPENAI_API_KEY": "test-only-openai",
    "ATI_OPENAI_REALTIME_MODEL": "gpt-realtime-2.1",
    "ATI_OPENAI_TRANSCRIPTION_MODEL": "gpt-transcribe",
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


def _verified(*ids: str) -> object:
    """The packaged registry with the given models marked verified (no model is, in this repository)."""
    raw = {
        "models": [
            {
                "id": m.id,
                "provider": m.provider,
                "purpose": m.purpose,
                "status": "verified" if m.id in ids else m.status,
                "shutdown_on": m.shutdown_on.isoformat() if m.shutdown_on else None,
                "replacements": list(m.replacements),
            }
            for m in registry().values()
        ]
    }
    return parse(raw)


def test_production_accepts_a_safe_configuration_with_verified_models() -> None:
    assert load(PROD, models=_verified("gpt-realtime-2.1", "gpt-transcribe")).production  # type: ignore[arg-type]


def test_production_refuses_models_without_live_verification() -> None:
    with pytest.raises(ConfigError, match="production requires a model verified against the live provider"):
        load(PROD)


def test_openai_models_must_be_named() -> None:
    env = {k: v for k, v in OPENAI.items() if not k.startswith("ATI_OPENAI_")}
    with pytest.raises(ConfigError, match="ATI_OPENAI_REALTIME_MODEL is required") as e:
        load(env)
    assert "ATI_OPENAI_TRANSCRIPTION_MODEL is required" in str(e.value)


def test_deprecated_models_warn_until_their_shutdown_then_are_refused() -> None:
    env = {**OPENAI, "ATI_OPENAI_REALTIME_MODEL": "gpt-realtime"}
    cfg = load(env, today=date(2026, 11, 4))
    assert any("gpt-realtime" in w and "2027-01-20" in w and "gpt-realtime-2.1" in w and "soon" in w for w in cfg.warnings), cfg.warnings
    with pytest.raises(ConfigError, match="shut down by the provider on 2027-01-20"):
        load(env, today=date(2027, 1, 20))


def test_unregistered_models_warn_in_dev_and_are_refused_in_staging() -> None:
    env = {**OPENAI, "ATI_OPENAI_REALTIME_MODEL": "gpt-realtime-typo"}
    assert any("not a registered openai tier1_realtime model" in w for w in load(env).warnings)
    staging = {**env, "APP_ENV": "staging", "ATI_LIVEKIT_URL": "wss://media.example.invalid"}
    with pytest.raises(ConfigError, match="not a registered openai tier1_realtime model"):
        load(staging)
    # A transcription model is not accepted as the realtime model either.
    with pytest.raises(ConfigError, match="not a registered openai tier1_realtime model"):
        load({**staging, "ATI_OPENAI_REALTIME_MODEL": "gpt-transcribe"})


def test_the_provider_endpoint_is_pinned() -> None:
    assert load(OPENAI).openai_base_url == "https://api.openai.com/v1"
    with pytest.raises(ConfigError, match="OPENAI_BASE_URL differs"):
        load({**OPENAI, "OPENAI_BASE_URL": "https://elsewhere.invalid/v1"})
    assert load({**OPENAI, "OPENAI_BASE_URL": "https://api.openai.com/v1/"}).openai_base_url == "https://api.openai.com/v1"
    assert load({**OPENAI, "ATI_OPENAI_BASE_URL": "http://127.0.0.1:9999/v1"}).openai_base_url == "http://127.0.0.1:9999/v1"
    with pytest.raises(ConfigError, match="must be an https URL"):
        load({**OPENAI, "ATI_OPENAI_BASE_URL": "ftp://example.invalid"})
    verified = _verified("gpt-realtime-2.1", "gpt-transcribe")
    with pytest.raises(ConfigError, match="must be an https URL"):
        load({**PROD, "ATI_OPENAI_BASE_URL": "http://api.openai.com/v1"}, models=verified)  # type: ignore[arg-type]
    with pytest.raises(ConfigError, match="pinned in ADR-021"):
        load({**PROD, "ATI_OPENAI_BASE_URL": "https://proxy.example.invalid/v1"}, models=verified)  # type: ignore[arg-type]


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
        load({**PROD, **override}, models=_verified("gpt-realtime-2.1", "gpt-transcribe"))  # type: ignore[arg-type]


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
