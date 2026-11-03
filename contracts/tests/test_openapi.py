"""OpenAPI contract checks (BLUEPRINT section 17 cross-cutting API rules; ADR-016)."""

from __future__ import annotations

from typing import Any

import pytest
import yaml
from openapi_spec_validator import validate
from openapi_spec_validator.readers import read_from_filename

from conftest import REPO

SPEC_PATH = REPO / "docs/contracts/openapi/control-plane.v1.yaml"
METHODS = {"get", "post", "put", "patch", "delete"}


@pytest.fixture(scope="module")
def spec() -> dict[str, Any]:
    return yaml.safe_load(SPEC_PATH.read_text(encoding="utf-8"))


def _ops(spec: dict[str, Any]):
    for path, item in spec["paths"].items():
        for method, op in item.items():
            if method in METHODS:
                yield path, method, op


def test_spec_is_valid_openapi_3_1_with_resolvable_external_refs() -> None:
    spec_dict, base_uri = read_from_filename(str(SPEC_PATH))
    validate(spec_dict, base_uri=base_uri)


def test_every_operation_declares_security_explicitly(spec: dict[str, Any]) -> None:
    for path, method, op in _ops(spec):
        assert "security" in op, f"{method.upper()} {path} must declare security (use [] for public)"
        if op["security"] == []:
            assert path in {"/api/v1/invitations/exchange", "/healthz", "/readyz", "/metrics"}, f"{path} is unauthenticated"


def test_public_mutations_require_idempotency_keys(spec: dict[str, Any]) -> None:
    for path, method, op in _ops(spec):
        if path.startswith("/api/v1") and method == "post":
            refs = [p.get("$ref", "") for p in op.get("parameters", [])]
            assert "#/components/parameters/IdempotencyKey" in refs, f"POST {path} lacks Idempotency-Key"


def test_lease_bound_internal_operations_require_fencing(spec: dict[str, Any]) -> None:
    for path, method, op in _ops(spec):
        if path.startswith("/internal/v1/tier1/sessions/"):
            refs = [p.get("$ref", "") for p in op.get("parameters", [])]
            assert "#/components/parameters/FencingToken" in refs, f"{path} lacks X-ATI-Fencing-Token"
            assert op["security"] == [{"agentService": [], "agentGrant": []}], f"{path} must require service + grant"


def test_internal_and_public_namespaces_do_not_mix(spec: dict[str, Any]) -> None:
    for path, _method, op in _ops(spec):
        tags = set(op["tags"])
        if path.startswith("/internal/"):
            assert tags <= {"tier1-internal", "ops"}, path
        elif path.startswith("/api/"):
            assert tags <= {"staff", "candidate"}, path


def test_api_exposes_no_scoring_or_decision_surface(spec: dict[str, Any]) -> None:
    text = SPEC_PATH.read_text(encoding="utf-8").lower()
    for path in spec["paths"]:
        assert not any(w in path for w in ("score", "evaluation", "rank", "hire", "reject", "decision")), path
    assert "weighted_score" not in text and "hiring_decision" not in text


def test_problem_responses_use_problem_json(spec: dict[str, Any]) -> None:
    problem = spec["components"]["responses"]["Problem"]
    assert list(problem["content"]) == ["application/problem+json"]
    for path, method, op in _ops(spec):
        for status, resp in op["responses"].items():
            if status.startswith(("4", "5")):
                assert resp == {"$ref": "#/components/responses/Problem"}, f"{method} {path} {status}"
