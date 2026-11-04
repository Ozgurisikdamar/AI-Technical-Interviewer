"""Worker flow: gates, submissions, completion and fallbacks (ADR-022)."""

from __future__ import annotations

import json
from collections.abc import Callable, Sequence
from dataclasses import replace
from typing import Any

import httpx
import pytest

from tier2_evaluator import config
from tier2_evaluator.adapters import Attempt, EvaluationTask, TransientEvaluatorError
from tier2_evaluator.adapters.reference import ReferenceEvaluator
from tier2_evaluator.consumer import RetryLater, Undeliverable
from tier2_evaluator.control_plane import ControlPlane, ControlPlaneError
from tier2_evaluator.evidence import SessionEvidence
from tier2_evaluator.worker import Worker, abstention

from .conftest import SESSION, answer, completed_fields, seg, session
from .test_reference import EN, control_plane_problems

CFG = config.load(
    {
        "APP_ENV": "test",
        "ATI_TIER2_EVALUATOR": "reference",
        "ATI_REDIS_URL": "redis://127.0.0.1:1/0",
        "ATI_TIER2_DATABASE_URL": "postgres://ati_tier2_evaluator@127.0.0.1:1/ati",
        "ATI_CONTROL_PLANE_INTERNAL_URL": "http://control-plane.test",
        "ATI_TIER2_SERVICE_TOKEN": "ati-dev-tier2-token",
    }
)
A1 = answer(*EN, answer_id="ans_0123456789abcdef01", item="q1")
A2 = answer(*EN, answer_id="ans_0123456789abcdef02", item="q2", flags=("synthetic_source",))
A3 = answer(*EN, answer_id="ans_0123456789abcdef03", item="q3", rubric_id=None, rubric_version=None)


class FakeControlPlane:
    def __init__(self, respond: Callable[[str, dict[str, Any]], httpx.Response] | None = None) -> None:
        self.requests: list[tuple[str, dict[str, Any]]] = []
        self._respond = respond

    def handler(self, request: httpx.Request) -> httpx.Response:
        assert request.headers["authorization"] == "Bearer ati-dev-tier2-token"
        body = json.loads(request.content)
        self.requests.append((request.url.path, body))
        if self._respond is not None:
            return self._respond(request.url.path, body)
        if request.url.path.endswith("/complete"):
            return httpx.Response(200, json={"state": "HUMAN_REVIEW", "review_tasks": 3})
        return httpx.Response(201, json={"attempt_id": "eva_x", "evaluation_id": None, "outcome": "valid", "problems": []})

    def client(self) -> ControlPlane:
        return ControlPlane(CFG.control_plane_url, CFG.service_token, transport=httpx.MockTransport(self.handler))

    def paths(self) -> list[str]:
        return [p.rsplit("/", 1)[-1] for p, _ in self.requests]


def loader(ev: SessionEvidence | None, seen: list[tuple[str, list[str]]] | None = None) -> Any:
    async def load(url: str, session_id: str, answer_ids: Sequence[str]) -> SessionEvidence | None:
        if seen is not None:
            seen.append((session_id, list(answer_ids)))
        return ev

    return load


def worker(
    ev: SessionEvidence | None, cp: FakeControlPlane, evaluator: Any = None, seen: list[tuple[str, list[str]]] | None = None
) -> Worker:
    return Worker(CFG, evaluator or ReferenceEvaluator(), cp.client(), load=loader(ev, seen))


async def test_completed_interview_is_evaluated_reported_and_routed_to_review() -> None:
    cp, seen = FakeControlPlane(), []
    ev = session(A1, A2, A3)
    await worker(ev, cp, seen=seen).handle(completed_fields([A1.answer_id, A2.answer_id, A3.answer_id]))
    assert seen == [(SESSION, [A1.answer_id, A2.answer_id, A3.answer_id])]
    assert cp.paths() == ["evaluations", "evaluations", "complete"]  # no rubric: nothing to submit for A3
    (_, first), (_, second), (_, done) = cp.requests
    assert first["answer_id"] == A1.answer_id and first["outcome"] == "evaluated"
    assert (first["evaluator_version"], first["provider"], first["model_id"], first["prompt_sha256"]) == (
        "reference@1",
        "reference",
        None,
        None,
    )
    assert control_plane_problems(first["output"], A1, ev.rubric_for(A1)) == []  # type: ignore[arg-type]
    assert second == {**second, "answer_id": A2.answer_id, "outcome": "abstained", "reason": "synthetic_source", "output": None}
    assert done == {"failure_reason": None}


async def test_other_events_cause_no_work() -> None:
    cp, seen = FakeControlPlane(), []
    await worker(session(A1), cp, seen=seen).handle({"event_type": "answer.finalized", "session_id": SESSION, "payload": "{}"})
    assert seen == [] and cp.requests == []


@pytest.mark.parametrize("state", ["HUMAN_REVIEW", "REPORT_READY", "QUESTION_ACTIVE", "CANCELLED"])
async def test_interviews_not_pending_evaluation_are_left_alone(state: str) -> None:
    cp = FakeControlPlane()
    await worker(session(A1, state=state), cp).handle(completed_fields([A1.answer_id]))
    assert cp.requests == []


async def test_unknown_session_is_acknowledged_without_work() -> None:
    cp = FakeControlPlane()
    await worker(None, cp).handle(completed_fields([A1.answer_id]))
    assert cp.requests == []


async def test_redelivery_skips_answers_this_evaluator_already_evaluated() -> None:
    cp = FakeControlPlane()
    done = frozenset({(A1.answer_id, 1, "rub_demo_postgres_mvcc", 1, "reference@1")})
    await worker(session(A1, evaluated=done), cp).handle(completed_fields([A1.answer_id]))
    assert cp.paths() == ["complete"]


@pytest.mark.parametrize(
    "fields",
    [
        {**completed_fields(["ans_0123456789abcdef01"]), "payload": "not json"},
        {**completed_fields(["ans_0123456789abcdef01"]), "payload": json.dumps({"event_type": "interview.completed"})},
        {**completed_fields(["ans_0123456789abcdef01"]), "session_id": "ses_ANOTHER0123456789ab"},
    ],
)
async def test_malformed_entries_are_undeliverable(fields: dict[str, str]) -> None:
    with pytest.raises(Undeliverable):
        await worker(session(A1), FakeControlPlane()).handle(fields)


def test_abstention_gates() -> None:
    assert abstention(A2) == "synthetic_source"
    assert abstention(answer(segments=())) == "no_candidate_speech"
    low = answer(segments=(seg(1, "uh the row version", confidence=0.4), seg(2, "snapshot", confidence=0.7)))
    assert abstention(low) == "transcript_quality_low"
    assert abstention(answer(segments=(seg(1, "typed answer"),))) is None  # text mode has no ASR confidence


async def test_transient_control_plane_failure_retries_the_delivery() -> None:
    cp = FakeControlPlane(lambda path, body: httpx.Response(503, json={"code": "unavailable"}))
    with pytest.raises(RetryLater):
        await worker(session(A1), cp).handle(completed_fields([A1.answer_id]))
    assert len(cp.requests) == 4  # the client retried 503 before giving the delivery back


async def test_transcript_revision_between_read_and_report_retries_the_delivery() -> None:
    cp = FakeControlPlane(lambda path, body: httpx.Response(409, json={"code": "transcript_version_mismatch"}))
    with pytest.raises(RetryLater, match="transcript_revised"):
        await worker(session(A1), cp).handle(completed_fields([A1.answer_id]))


async def test_interview_that_moved_on_stops_the_entry_quietly() -> None:
    cp = FakeControlPlane(lambda path, body: httpx.Response(409, json={"code": "evaluation_not_pending"}))
    await worker(session(A1, replace(A1, answer_id="ans_0123456789abcdef09")), cp).handle(completed_fields([A1.answer_id]))
    assert cp.paths() == ["evaluations"]  # neither the second answer nor completion was attempted


async def test_refused_attempt_does_not_block_the_other_answers() -> None:
    def respond(path: str, body: dict[str, Any]) -> httpx.Response:
        if path.endswith("/complete"):
            return httpx.Response(200, json={"state": "HUMAN_REVIEW", "review_tasks": 2})
        if body["answer_id"] == A1.answer_id:
            return httpx.Response(400, json={"code": "invalid_request"})
        return httpx.Response(201, json={"attempt_id": "eva_x", "outcome": "abstained", "problems": ["synthetic_source"]})

    cp = FakeControlPlane(respond)
    await worker(session(A1, A2), cp).handle(completed_fields([A1.answer_id, A2.answer_id]))
    assert cp.paths() == ["evaluations", "evaluations", "complete"]


class FlakyEvaluator(ReferenceEvaluator):
    async def evaluate(self, task: EvaluationTask) -> Attempt:
        raise TransientEvaluatorError("RateLimitError")


async def test_provider_overload_retries_the_delivery() -> None:
    cp = FakeControlPlane()
    with pytest.raises(RetryLater, match="evaluator_RateLimitError"):
        await worker(session(A1), cp, FlakyEvaluator()).handle(completed_fields([A1.answer_id]))
    assert cp.requests == []


async def test_dead_letter_routes_the_interview_to_review_with_a_failure_reason() -> None:
    cp = FakeControlPlane()
    await worker(session(A1), cp).dead_letter(completed_fields([A1.answer_id]), "max_deliveries")
    assert cp.requests == [(f"/internal/v1/tier2/sessions/{SESSION}/complete", {"failure_reason": "dead_lettered"})]
    other = FakeControlPlane()
    await worker(session(A1), other).dead_letter({"event_type": "answer.finalized", "session_id": SESSION}, "max_deliveries")
    assert other.requests == []


async def test_dead_letter_report_conflict_is_final_but_an_outage_is_retried() -> None:
    conflict = FakeControlPlane(lambda path, body: httpx.Response(409, json={"code": "evaluation_not_pending"}))
    await worker(session(A1), conflict).dead_letter(completed_fields([A1.answer_id]), "max_deliveries")
    outage = FakeControlPlane(lambda path, body: httpx.Response(502, json={"code": "bad_gateway"}))
    with pytest.raises(ControlPlaneError):
        await worker(session(A1), outage).dead_letter(completed_fields([A1.answer_id]), "max_deliveries")
