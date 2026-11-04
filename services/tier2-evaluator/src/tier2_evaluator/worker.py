"""Tier-2 worker (ADR-022).

For each interview.completed entry: read the interview's evidence in one read-only snapshot,
evaluate every answer that has a stored rubric (or record why the evaluator abstained), report each
attempt to the control plane, then ask it to route the interview to human review (T15). Every other
event type is acknowledged without work: Tier-2 never acts on a live interview (INV-6).

Redelivery is safe. Answers that already have a canonical evaluation from this evaluator version
are skipped, the control plane deduplicates the rest, and completing twice changes nothing.
"""

from __future__ import annotations

import asyncio
import json
import logging
import signal
from collections.abc import Awaitable, Callable, Sequence
from functools import cache
from importlib import resources
from typing import Any

import psycopg
import redis.asyncio as aioredis
from jsonschema import Draft202012Validator

from . import evidence
from .adapters import Attempt, EvaluationTask, Evaluator, TransientEvaluatorError, build
from .config import Config
from .consumer import Consumer, RetryLater, Undeliverable
from .control_plane import ControlPlane, ControlPlaneError

log = logging.getLogger("tier2.worker")

# Below this mean ASR confidence the evaluator abstains (PROMPT_AND_EVALUATION section 6:
# inadequate transcript quality goes to human review). Uncalibrated policy value.
TRANSCRIPT_CONFIDENCE_FLOOR = 0.6

Loader = Callable[[str, str, Sequence[str]], Awaitable[evidence.SessionEvidence | None]]


@cache
def _completed_validator() -> Draft202012Validator:
    raw = resources.files("tier2_evaluator.contracts").joinpath("interview-completed.v1.schema.json").read_text(encoding="utf-8")
    return Draft202012Validator(json.loads(raw))


def abstention(answer: evidence.AnswerEvidence) -> str | None:
    """Why the evaluator must not evaluate this answer, if it must not."""
    if "synthetic_source" in answer.integrity_flags:
        return "synthetic_source"  # synthetic test speech is never evaluated
    if not answer.segments:
        return "no_candidate_speech"
    confidences = [s.confidence for s in answer.segments if s.confidence is not None]
    if confidences and sum(confidences) / len(confidences) < TRANSCRIPT_CONFIDENCE_FLOOR:
        return "transcript_quality_low"
    return None


class _SessionMovedOn(Exception):
    """The interview is no longer pending evaluation; this entry has nothing left to do."""


class Worker:
    def __init__(self, cfg: Config, evaluator: Evaluator, control_plane: ControlPlane, load: Loader | None = None) -> None:
        self._cfg, self._evaluator, self._cp = cfg, evaluator, control_plane
        self._load: Loader = load or evidence.load

    async def handle(self, fields: dict[str, str]) -> None:
        if fields.get("event_type") != "interview.completed":
            return
        try:
            payload = json.loads(fields.get("payload", ""))
        except ValueError:
            raise Undeliverable("malformed_event") from None
        if not isinstance(payload, dict) or any(True for _ in _completed_validator().iter_errors(payload)):
            raise Undeliverable("malformed_event")
        session_id: str = payload["session_id"]
        if fields.get("session_id") not in (None, session_id):
            raise Undeliverable("malformed_event")
        try:
            ev = await self._load(self._cfg.database_url, session_id, payload["answer_ids"])
        except (psycopg.OperationalError, OSError):
            raise RetryLater("evidence_unavailable") from None  # the database may be restarting
        if ev is None:
            log.warning("interview unknown to the evaluator; nothing to do", extra={"session_id": session_id})
            return
        if ev.state in ("HUMAN_REVIEW", "REPORT_READY"):
            log.info("interview already routed to review", extra={"session_id": session_id, "state": ev.state})
            return
        if ev.state != "EVALUATION_PENDING":
            log.warning("interview is not pending evaluation; nothing to do", extra={"session_id": session_id, "state": ev.state})
            return
        try:
            for answer in ev.answers:
                await self._answer(ev, answer)
            routing = await self._cp.complete(session_id)
        except _SessionMovedOn:
            log.info("interview moved on during evaluation", extra={"session_id": session_id})
            return
        except ControlPlaneError as e:
            if e.transient:
                raise RetryLater(f"control_plane_{e.status}") from None
            if e.code == "evaluation_not_pending":
                return
            raise
        log.info("interview routed to human review", extra={"session_id": session_id, "review_tasks": routing.get("review_tasks")})

    async def _answer(self, ev: evidence.SessionEvidence, answer: evidence.AnswerEvidence) -> None:
        ctx = {"session_id": ev.session_id, "answer_id": answer.answer_id}
        if answer.rubric_id is None:
            log.info("answer has no rubric; left to the reviewer", extra=ctx)
            return
        rubric = ev.rubric_for(answer)
        if rubric is None:
            log.error("answer's rubric is not stored; left to the reviewer", extra=ctx)
            return
        key = (answer.answer_id, answer.transcript_version, rubric.rubric_id, rubric.version, self._evaluator.version)
        if key in ev.evaluated:
            return  # a redelivery: this evaluator already produced the canonical evaluation
        reason = abstention(answer)
        if reason is not None:
            attempt = Attempt("abstained", reason=reason)
        else:
            try:
                attempt = await self._evaluator.evaluate(EvaluationTask(answer, rubric, ev.language))
            except TransientEvaluatorError as e:
                raise RetryLater(f"evaluator_{e}") from None
        try:
            result = await self._cp.record_evaluation(ev.session_id, self._submission(answer, attempt))
        except ControlPlaneError as e:
            if e.transient:
                raise
            if e.code == "evaluation_not_pending":
                raise _SessionMovedOn from None
            if e.code == "transcript_version_mismatch":
                raise RetryLater("transcript_revised") from None  # reload the new version on redelivery
            log.error("attempt refused by the control plane", extra={**ctx, "status": e.status, "code": e.code})
            return
        log.info(
            "attempt recorded",
            extra={
                **ctx,
                "attempt_outcome": result.get("outcome"),
                "problems": len(result.get("problems") or []),
                "evaluator": self._evaluator.version,
            },
        )

    def _submission(self, answer: evidence.AnswerEvidence, attempt: Attempt) -> dict[str, Any]:
        return {
            "answer_id": answer.answer_id,
            "transcript_version": answer.transcript_version,
            "evaluator_version": self._evaluator.version,
            "provider": self._evaluator.provider,
            "model_id": self._evaluator.model_id,
            "prompt_sha256": self._evaluator.prompt_sha256,
            "provider_request_id": attempt.provider_request_id,
            "outcome": attempt.outcome,
            "output": attempt.output if attempt.outcome == "evaluated" else None,
            "reason": attempt.reason,
            "latency_ms": attempt.latency_ms,
            "input_tokens": attempt.input_tokens,
            "output_tokens": attempt.output_tokens,
        }

    async def dead_letter(self, fields: dict[str, str], reason: str) -> None:
        """The pipeline gave up on an entry: route its interview to human review anyway."""
        session_id = fields.get("session_id")
        if fields.get("event_type") != "interview.completed" or not session_id:
            return
        try:
            await self._cp.complete(session_id, "dead_lettered")
        except ControlPlaneError as e:
            if e.transient:
                raise
            log.warning("dead-lettered interview could not be routed", extra={"session_id": session_id, "status": e.status, "code": e.code})
        else:
            log.warning("dead-lettered interview routed to human review", extra={"session_id": session_id, "reason": reason})


async def run(cfg: Config) -> None:
    client = aioredis.from_url(cfg.redis_url, socket_connect_timeout=5, socket_timeout=15, health_check_interval=30)
    evaluator = build(cfg)
    cp = ControlPlane(cfg.control_plane_url, cfg.service_token)
    stop = asyncio.Event()
    loop = asyncio.get_running_loop()
    for sig in (signal.SIGTERM, signal.SIGINT):
        loop.add_signal_handler(sig, stop.set)
    consumer = Consumer(
        client,
        Worker(cfg, evaluator, cp),
        consumer_name=cfg.consumer_name,
        max_deliveries=cfg.max_deliveries,
        visibility_timeout_ms=cfg.visibility_timeout_ms,
    )
    log.info("tier-2 worker started", extra={"evaluator": evaluator.version, "consumer": cfg.consumer_name, "app_env": cfg.app_env})
    try:
        await consumer.run(stop)
    finally:
        await evaluator.aclose()
        await cp.aclose()
        await client.aclose()
        log.info("tier-2 worker stopped")
