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


UNAUTHENTICATED = {
    ("post", "/api/v1/invitations/exchange"),
    ("post", "/api/v1/staff/session"),  # sign-in: the body carries the staff token (ADR-023)
    ("get", "/healthz"),
    ("get", "/readyz"),
    ("get", "/metrics"),
}


def test_every_operation_declares_security_explicitly(spec: dict[str, Any]) -> None:
    for path, method, op in _ops(spec):
        assert "security" in op, f"{method.upper()} {path} must declare security (use [] for public)"
        if op["security"] == []:
            assert (method, path) in UNAUTHENTICATED, f"{method.upper()} {path} is unauthenticated"


def test_the_staff_browser_session_opens_only_review_reports_and_itself(spec: dict[str, Any]) -> None:
    """The review console's cookie (ADR-023) never reaches interview management or candidate operations, and is
    always an alternative to the bearer token, never a requirement."""
    allowed = []
    for path, method, op in _ops(spec):
        schemes = [set(req) for req in op["security"]]
        if not any("staffSession" in req for req in schemes):
            continue
        assert "review" in op["tags"] or path.startswith("/api/v1/staff/session"), f"{method.upper()} {path}"
        assert {"staffSession"} in schemes and {"staffBearer"} in schemes, f"{method.upper()} {path}"
        allowed.append((method, path))
    assert ("post", "/api/v1/review-tasks/{task_id}/outcome") in allowed
    assert ("get", "/api/v1/staff/session") in allowed
    for method, path in [("post", "/api/v1/interviews"), ("get", "/api/v1/interviews/{interview_id}")]:
        assert (method, path) not in allowed, f"{method.upper()} {path}"


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
        schemes = {name for req in op["security"] for name in req}
        if path.startswith("/internal/"):
            assert tags <= {"tier1-internal", "tier2-internal", "ops"}, path
        elif path.startswith("/api/"):
            assert tags <= {"staff", "candidate", "review"}, path
        # The Tier-1 and Tier-2 credentials are separate identities (ADR-001, ADR-022).
        if path.startswith("/internal/v1/tier2/"):
            assert tags == {"tier2-internal"} and schemes == {"tier2Service"}, path
        else:
            assert "tier2Service" not in schemes, path
        if "tier2-internal" in tags or "review" in tags:
            assert not schemes & {"agentService", "agentGrant", "candidateSession"}, path


def _properties(spec: dict[str, Any], node: Any, seen: set[str] | None = None) -> set[str]:
    """Every property name reachable from a schema (following $ref, allOf, items, nested objects)."""
    seen = set() if seen is None else seen
    out: set[str] = set()
    if isinstance(node, dict):
        ref = node.get("$ref")
        if isinstance(ref, str) and ref.startswith("#/components/schemas/"):
            if ref in seen:
                return out
            seen.add(ref)
            return _properties(spec, spec["components"]["schemas"][ref.rsplit("/", 1)[1]], seen)
        for name, child in node.get("properties", {}).items():
            out.add(name)
            out |= _properties(spec, child, seen)
        for key in ("allOf", "anyOf", "oneOf"):
            for child in node.get(key, []):
                out |= _properties(spec, child, seen)
        if "items" in node:
            out |= _properties(spec, node["items"], seen)
    return out


def _operation_properties(spec: dict[str, Any], op: dict[str, Any]) -> set[str]:
    names: set[str] = set()
    bodies = [op.get("requestBody", {})] + list(op.get("responses", {}).values())
    for body in bodies:
        if "$ref" in body:
            body = spec["components"]["responses"][body["$ref"].rsplit("/", 1)[1]]
        for media in body.get("content", {}).values():
            names |= _properties(spec, media.get("schema", {}))
    return names


def test_api_exposes_no_hiring_decision_surface(spec: dict[str, Any]) -> None:
    """No path, operation or field makes, ranks or recommends a hiring decision (ADR-008, ADR-022)."""
    text = SPEC_PATH.read_text(encoding="utf-8").lower()
    assert "hiring_decision" not in text
    for path, _method, op in _ops(spec):
        assert not any(w in path for w in ("score", "rank", "hire", "reject", "decision")), path
        if "evaluation" in path:
            assert path.startswith("/internal/v1/tier2/"), f"{path}: evaluations are reported only by the Tier-2 service"
        for name in _operation_properties(spec, op):
            assert not any(w in name.lower() for w in ("hire", "hiring", "reject", "rank", "recommend")), f"{path}: field {name}"


def test_scores_appear_only_in_staff_review_and_tier2_operations(spec: dict[str, Any]) -> None:
    """Rubric scores exist only where a human reviews them or the Tier-2 service reports them: never in a
    candidate-facing or Tier-1 operation (ADR-022)."""
    scored = []
    for path, method, op in _ops(spec):
        names = _operation_properties(spec, op)
        if any("score" in n for n in names):
            assert set(op["tags"]) <= {"review", "tier2-internal"}, f"{method.upper()} {path} exposes scores"
            scored.append(path)
    assert "/api/v1/interviews/{interview_id}/report" in scored and "/api/v1/review-tasks/{task_id}" in scored


def test_problem_responses_use_problem_json(spec: dict[str, Any]) -> None:
    problem = spec["components"]["responses"]["Problem"]
    assert list(problem["content"]) == ["application/problem+json"]
    for path, method, op in _ops(spec):
        for status, resp in op["responses"].items():
            if status.startswith(("4", "5")):
                assert resp == {"$ref": "#/components/responses/Problem"}, f"{method} {path} {status}"
