"""Read-only evidence loader (ADR-022 decision 3).

The worker connects as ati_tier2_evaluator, which may read answers, transcript segments,
questions, rubrics and two narrow views, and may write nothing. It never sees candidate identity.
Everything an interview's evaluation needs is read in one short read-only snapshot, before any
evaluator runs, so no transaction stays open while a provider answers.

Segments are selected exactly as the control plane's validator selects them: the answer's
evidence-eligible segments of its transcript version, in transcript order.
"""

from __future__ import annotations

import json
from collections.abc import Sequence
from dataclasses import dataclass
from typing import Any

import psycopg
from psycopg import IsolationLevel


@dataclass(frozen=True)
class Segment:
    id: str
    speaker: str
    start_ms: int
    end_ms: int
    text: str
    confidence: float | None


@dataclass(frozen=True)
class AnswerEvidence:
    session_id: str
    answer_id: str
    item_id: str
    transcript_version: int
    question: str
    rubric_id: str | None
    rubric_version: int | None
    segments: tuple[Segment, ...]
    missing_segments: bool
    integrity_flags: tuple[str, ...]


@dataclass(frozen=True)
class StoredRubric:
    rubric_id: str
    version: int
    synthetic: bool
    body: dict[str, Any]


@dataclass(frozen=True)
class SessionEvidence:
    session_id: str
    tenant_id: str
    state: str
    language: str
    answers: tuple[AnswerEvidence, ...]
    rubrics: dict[tuple[str, int], StoredRubric]
    # (answer_id, transcript_version, rubric_id, rubric_version, evaluator_version) already evaluated
    evaluated: frozenset[tuple[str, int, str, int, str]]

    def rubric_for(self, answer: AnswerEvidence) -> StoredRubric | None:
        if answer.rubric_id is None or answer.rubric_version is None:
            return None
        return self.rubrics.get((answer.rubric_id, answer.rubric_version))


async def load(database_url: str, session_id: str, answer_ids: Sequence[str]) -> SessionEvidence | None:
    """The session's evidence, or None when the session is unknown to the evaluator."""
    async with await psycopg.AsyncConnection.connect(database_url, connect_timeout=5) as conn:
        await conn.set_isolation_level(IsolationLevel.REPEATABLE_READ)
        await conn.set_read_only(True)
        async with conn.transaction(), conn.cursor() as cur:
            await cur.execute(
                "SELECT session_id, tenant_id, state, plan_id, plan_version, language FROM ati.tier2_session_v WHERE session_id = %s",
                (session_id,),
            )
            row = await cur.fetchone()
            if row is None:
                return None
            _, tenant_id, state, plan_id, plan_version, language = row
            await cur.execute(
                """SELECT a.id, a.item_id, a.transcript_version, a.segment_ids, a.missing_segments, a.integrity_flags,
                          q.text, q.rubric_id, q.rubric_version
                   FROM ati.candidate_answers a
                   JOIN ati.interview_questions q ON q.plan_id = %s AND q.plan_version = %s AND q.item_id = a.item_id
                   WHERE a.session_id = %s AND a.id = ANY(%s)
                   ORDER BY a.finalized_at, a.id""",
                (plan_id, plan_version, session_id, list(answer_ids)),
            )
            answer_rows = await cur.fetchall()
            answers: list[AnswerEvidence] = []
            for answer_id, item_id, tv, segment_ids, missing, flags, question, rubric_id, rubric_version in answer_rows:
                await cur.execute(
                    """SELECT id, speaker, start_ms, end_ms, text, confidence FROM ati.transcript_segments
                       WHERE session_id = %s AND id = ANY(%s) AND transcript_version = %s AND evidence_eligible
                       ORDER BY start_ms, sequence""",
                    (session_id, list(segment_ids), tv),
                )
                segments = tuple(
                    Segment(sid, speaker, int(start), int(end), text, conf) for sid, speaker, start, end, text, conf in await cur.fetchall()
                )
                answers.append(
                    AnswerEvidence(
                        session_id, answer_id, item_id, tv, question, rubric_id, rubric_version, segments, missing, tuple(sorted(flags))
                    )
                )
            rubrics: dict[tuple[str, int], StoredRubric] = {}
            for key in {(a.rubric_id, a.rubric_version) for a in answers if a.rubric_id is not None and a.rubric_version is not None}:
                await cur.execute("SELECT body, synthetic FROM ati.rubrics WHERE rubric_id = %s AND version = %s", key)
                found = await cur.fetchone()
                if found is not None:
                    body = found[0] if isinstance(found[0], dict) else json.loads(found[0])
                    rubrics[key] = StoredRubric(key[0], key[1], bool(found[1]), body)
            await cur.execute(
                """SELECT answer_id, transcript_version, rubric_id, rubric_version, evaluator_version
                   FROM ati.tier2_evaluation_keys_v WHERE answer_id = ANY(%s)""",
                ([a.answer_id for a in answers],),
            )
            evaluated = frozenset((r[0], int(r[1]), r[2], int(r[3]), r[4]) for r in await cur.fetchall())
    return SessionEvidence(session_id, tenant_id, state, language, tuple(answers), rubrics, evaluated)
