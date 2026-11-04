"""Configuration refusals (ADR-021, ADR-022)."""

from __future__ import annotations

from datetime import date

import pytest

from tier2_evaluator import config
from tier2_evaluator.models import RegisteredModel

DEV = {
    "APP_ENV": "dev",
    "ATI_TIER2_EVALUATOR": "reference",
    "ATI_REDIS_URL": "redis://127.0.0.1:6379/0",
    "ATI_TIER2_DATABASE_URL": "postgres://ati_tier2_evaluator@127.0.0.1:5432/ati?sslmode=disable",
    "ATI_CONTROL_PLANE_INTERNAL_URL": "http://127.0.0.1:8081/",
    "ATI_TIER2_SERVICE_TOKEN": "ati-dev-tier2-service-token",
}
PROD = {
    **DEV,
    "APP_ENV": "production",
    "ATI_TIER2_EVALUATOR": "anthropic",
    "ATI_TIER2_MODEL": "claude-test-verified",
    "ANTHROPIC_API_KEY": "key-from-a-secret-store",
    "ATI_REDIS_URL": "rediss://redis.internal:6380/0",
    "ATI_TIER2_DATABASE_URL": "postgres://ati_tier2_evaluator@db.example.invalid:5432/ati?sslmode=verify-full",
    "ATI_CONTROL_PLANE_INTERNAL_URL": "https://control-plane.internal:8443",
    "ATI_TIER2_SERVICE_TOKEN": "a-real-service-token-from-a-secret-store",
}
TODAY = date(2026, 11, 4)


def registry(**models: RegisteredModel) -> dict[str, RegisteredModel]:
    base = {
        "claude-test-verified": RegisteredModel("claude-test-verified", "anthropic", "verified", None, ()),
        "claude-test-candidate": RegisteredModel("claude-test-candidate", "anthropic", "candidate", None, ()),
        "gpt-test-deprecated": RegisteredModel("gpt-test-deprecated", "openai", "deprecated", date(2027, 1, 20), ("gpt-test-next",)),
        "gpt-test-gone": RegisteredModel("gpt-test-gone", "openai", "deprecated", date(2026, 10, 1), ("gpt-test-next",)),
    }
    return {**base, **models}


def load(env: dict[str, str]) -> config.Config:
    return config.load(env, today=TODAY, models=registry())


def refused(env: dict[str, str]) -> str:
    with pytest.raises(config.ConfigError) as e:
        load(env)
    return str(e.value)


def test_dev_reference_configuration_loads() -> None:
    cfg = load(DEV)
    assert cfg.evaluator == "reference" and cfg.model_id is None and cfg.api_key is None
    assert cfg.control_plane_url == "http://127.0.0.1:8081"
    assert (cfg.max_deliveries, cfg.visibility_timeout_ms, cfg.consumer_name) == (5, 120_000, "tier2-1")


def test_production_configuration_loads() -> None:
    cfg = load(PROD)
    assert cfg.production and cfg.model_id == "claude-test-verified" and cfg.base_url == "https://api.anthropic.com"


def test_unknown_environment_is_refused() -> None:
    assert "APP_ENV" in refused({**DEV, "APP_ENV": "prod"})


@pytest.mark.parametrize(
    ("change", "message"),
    [
        ({"ATI_TIER2_EVALUATOR": "reference"}, "reference evaluator"),
        ({"ATI_REDIS_URL": "redis://redis.internal:6379/0"}, "rediss://"),
        ({"ATI_CONTROL_PLANE_INTERNAL_URL": "http://control-plane.internal:8081"}, "https control-plane"),
        ({"ATI_TIER2_SERVICE_TOKEN": "ati-dev-tier2-service-token"}, "development placeholder"),
        ({"ATI_TIER2_DATABASE_URL": "postgres://ati_tier2_evaluator@db.example.invalid/ati?sslmode=require"}, "verify-full"),
        ({"ATI_TIER2_MODEL": "claude-test-candidate"}, "production requires a model verified"),
        ({"ATI_TIER2_BASE_URL": "https://proxy.example.invalid"}, "pinned in ADR-021"),
    ],
)
def test_production_refusals(change: dict[str, str], message: str) -> None:
    assert message in refused({**PROD, **change})


def test_every_problem_is_reported_at_once() -> None:
    msg = refused({**PROD, "ATI_TIER2_EVALUATOR": "reference", "ATI_REDIS_URL": "redis://x:1/0", "ATI_TIER2_SERVICE_TOKEN": "ati-dev-x"})
    assert "reference evaluator" in msg and "rediss://" in msg and "development placeholder" in msg


def test_model_evaluators_need_a_model_and_a_key() -> None:
    msg = refused({**DEV, "ATI_TIER2_EVALUATOR": "openai"})
    assert "ATI_TIER2_MODEL is required" in msg and "OPENAI_API_KEY" in msg


def test_unregistered_model_is_a_warning_in_dev_and_refused_in_staging() -> None:
    env = {**DEV, "ATI_TIER2_EVALUATOR": "openai", "ATI_TIER2_MODEL": "gpt-unknown", "OPENAI_API_KEY": "k"}
    assert any("not a registered openai evaluator model" in w for w in load(env).warnings)
    assert "not a registered openai evaluator model" in refused({**env, "APP_ENV": "staging"})


def test_model_of_another_provider_is_not_accepted() -> None:
    env = {**DEV, "APP_ENV": "staging", "ATI_TIER2_EVALUATOR": "openai", "ATI_TIER2_MODEL": "claude-test-verified", "OPENAI_API_KEY": "k"}
    assert "not a registered openai evaluator model" in refused(env)


def test_deprecated_model_warns_and_a_shut_down_model_is_refused() -> None:
    env = {**DEV, "ATI_TIER2_EVALUATOR": "openai", "OPENAI_API_KEY": "k"}
    warnings = load({**env, "ATI_TIER2_MODEL": "gpt-test-deprecated"}).warnings
    assert any("deprecated and shuts down on 2027-01-20 (77 days)" in w for w in warnings)
    assert "shut down by the provider on 2026-10-01; use gpt-test-next" in refused({**env, "ATI_TIER2_MODEL": "gpt-test-gone"})


@pytest.mark.parametrize(
    ("change", "message"),
    [
        ({"ATI_TIER2_BASE_URL": "http://api.anthropic.com"}, "must be an https URL"),
        ({"ANTHROPIC_BASE_URL": "https://elsewhere.example.invalid"}, "refusing an ambiguous provider endpoint"),
        ({"ANTHROPIC_CUSTOM_HEADERS": "anthropic-beta: something"}, "ANTHROPIC_CUSTOM_HEADERS is not supported"),
    ],
)
def test_provider_endpoint_and_headers_are_pinned(change: dict[str, str], message: str) -> None:
    env = {**DEV, "ATI_TIER2_EVALUATOR": "anthropic", "ATI_TIER2_MODEL": "claude-test-verified", "ANTHROPIC_API_KEY": "k"}
    assert message in refused({**env, **change})


def test_matching_inherited_base_url_is_accepted() -> None:
    env = {**DEV, "ATI_TIER2_EVALUATOR": "openai", "ATI_TIER2_MODEL": "gpt-test-deprecated", "OPENAI_API_KEY": "k"}
    assert load({**env, "OPENAI_BASE_URL": "https://api.openai.com/v1/"}).base_url == "https://api.openai.com/v1"


@pytest.mark.parametrize(
    ("key", "value"), [("ATI_TIER2_MAX_DELIVERIES", "0"), ("ATI_TIER2_MAX_DELIVERIES", "x"), ("ATI_TIER2_VISIBILITY_TIMEOUT_MS", "10")]
)
def test_bounds(key: str, value: str) -> None:
    assert key in refused({**DEV, key: value})


@pytest.mark.parametrize("key", ["ATI_REDIS_URL", "ATI_TIER2_DATABASE_URL", "ATI_CONTROL_PLANE_INTERNAL_URL", "ATI_TIER2_SERVICE_TOKEN"])
def test_required_settings(key: str) -> None:
    assert f"{key} is required" in refused({k: v for k, v in DEV.items() if k != key})


def test_shipped_registry_lists_the_documented_evaluator_candidates() -> None:
    from tier2_evaluator.models import registry as shipped

    models = shipped()
    assert {m.id for m in models.values()} >= {"claude-opus-5-5", "gpt-6.1-sol"}
    assert all(m.status in ("candidate", "verified", "deprecated") for m in models.values())
    # Nothing has been verified against a live provider yet, so production cannot start.
    env = {**PROD, "ATI_TIER2_MODEL": "claude-opus-5-5"}
    with pytest.raises(config.ConfigError, match="production requires a model verified"):
        config.load(env, today=TODAY)
