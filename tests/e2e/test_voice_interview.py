"""Voice interview over real WebRTC: LiveKit OSS + control plane + Tier-1 agent (scripted engine).

Results are "scripted engine, loopback only": they exercise the real media, dispatch, lease,
webhook, ingestion and state-machine paths, but not a model provider or a real network.
"""

from __future__ import annotations

import asyncio
import json
import statistics
import time

import pytest

from candidate import DONE, FAIL_LLM, INJECT, LONG, PART, POST_INTERVIEW, Candidate, answer, calibrate, listen_to_prompt
from conftest import Db, stack_cmd

pytestmark = pytest.mark.asyncio(loop_scope="session")

ANSWER_FINALIZED_KEYS = {
    "answer_id",
    "answer_version",
    "event_id",
    "event_type",
    "event_version",
    "finalization_cause",
    "integrity_flags",
    "item_id",
    "last_sequence",
    "missing_segments",
    "occurred_at",
    "parent_item_id",
    "plan",
    "rubric_ref",
    "segment_ids",
    "session_id",
    "tenant_id",
    "transcript_version",
}


async def test_full_interview_follows_the_plan_and_hands_off_ids_only(candidate: Candidate, db: Db) -> None:
    c = candidate
    view = await calibrate(c)
    await listen_to_prompt(c)
    items = []
    for _ in range(6):
        items.append(view["active_prompt"]["kind"])
        view = await answer(c)
        if view["state"] in POST_INTERVIEW:
            break
    assert view["state"] in POST_INTERVIEW, view
    assert items == ["question", "follow_up", "question", "question"]
    # The closing remark plays, then the agent leaves and the room is deleted (candidate disconnected).
    await c.wait_agent_speaks(timeout=10)
    await asyncio.wait_for(c.disconnected.wait(), 30)

    sid = c.interview_id
    answers = db.rows(
        """SELECT item_id, answer_version, finalization_cause, cardinality(segment_ids)
                         FROM ati.candidate_answers WHERE session_id = %s ORDER BY finalized_at""",
        sid,
    )
    assert [(r[0], r[1], r[2]) for r in answers] == [
        ("q1", 1, "agent_tool"),
        ("q1f1", 1, "agent_tool"),
        ("q2", 1, "agent_tool"),
        ("q3", 1, "agent_tool"),
    ]
    assert all(r[3] >= 1 for r in answers)
    events = db.rows("SELECT event_type, payload FROM ati.outbox_events WHERE session_id = %s ORDER BY seq", sid)
    assert [e[0] for e in events] == ["answer.finalized"] * 4 + ["interview.completed"]
    for _, payload in events:
        text = json.dumps(payload)
        assert "synthetic answer" not in text and "text" not in payload  # identifiers only, never transcript text
    assert set(events[0][1]) == ANSWER_FINALIZED_KEYS
    segs = db.rows(
        """SELECT speaker, source, evidence_eligible, (flags->>'unassigned')::bool FROM ati.transcript_segments
                      WHERE session_id = %s ORDER BY sequence""",
        sid,
    )
    candidate_segs = [s for s in segs if s[0] == "candidate"]
    assert all(s[1] == "synthetic_test" for s in candidate_segs)
    assert candidate_segs[0][2] is False  # calibration speech is never evidence
    assert sum(1 for s in candidate_segs if s[2]) >= 8  # two bursts per answer
    assert any(s[0] == "interviewer" for s in segs)
    transitions = {r[0] for r in db.rows("SELECT transition_id FROM ati.session_events WHERE session_id = %s", sid)}
    assert {"T04", "T05", "T06", "T07", "T08", "T09", "T10", "T11"} <= transitions
    assert db.one("SELECT count(*) FROM ati.audit_logs WHERE subject_id = %s AND action = 'media.room_deleted'", sid) == 1
    usage = db.rows("SELECT engine FROM ati.session_usage WHERE session_id = %s", sid)
    assert usage and usage[0][0] == "scripted"
    # Tier-2 (ADR-022): synthetic test speech is never evaluated, and every answer still reaches a reviewer.
    deadline = time.monotonic() + 30
    while db.one("SELECT state FROM ati.interview_sessions WHERE id = %s", sid) != "HUMAN_REVIEW":
        assert time.monotonic() < deadline, "the interview was not routed to human review"
        await asyncio.sleep(0.2)
    attempts = db.rows("SELECT outcome, problems FROM ati.evaluation_attempts WHERE session_id = %s", sid)
    assert len(attempts) == 4 and all(a == ("abstained", ["synthetic_source"]) for a in attempts), attempts
    assert db.one("SELECT count(*) FROM ati.evaluations WHERE session_id = %s", sid) == 0
    assert db.one("SELECT count(*) FROM ati.review_tasks WHERE session_id = %s AND 'synthetic_source' = ANY(reasons)", sid) == 4


async def test_barge_in_stops_the_interviewer_and_keeps_the_answer_open(candidate: Candidate, db: Db) -> None:
    c = candidate
    await calibrate(c)
    await listen_to_prompt(c)
    await c.say(LONG)
    await c.wait_state("CANDIDATE_SPEAKING", timeout=15)
    latencies = []
    for _ in range(5):
        started = await c.wait_agent_speaks(timeout=15)  # the neutral reply (about 2.6 s)
        await asyncio.sleep(0.6)
        onset = await c.say(PART)
        stop = c.audio.stopped_after(onset)
        assert stop is not None, "the interviewer kept speaking over the candidate"
        latencies.append((stop - onset) * 1000)
        assert stop - started < 2.4  # stopped before the reply would have ended
        await asyncio.sleep(0.4)
    latencies.sort()
    p50, p95 = statistics.median(latencies), latencies[-1]
    print(f"\nbarge-in stop latency (scripted engine, loopback): n={len(latencies)} p50={p50:.0f}ms max={p95:.0f}ms")
    assert p95 < 1500
    view = await c.view()
    assert view["state"] == "CANDIDATE_SPEAKING"  # barge-in never closes the answer
    interrupted = db.one(
        """SELECT count(*) FROM ati.transcript_segments WHERE session_id = %s AND speaker = 'interviewer'
                            AND interrupted""",
        c.interview_id,
    )
    assert interrupted >= 3
    assert db.one("SELECT count(*) FROM ati.candidate_answers WHERE session_id = %s", c.interview_id) == 0


async def test_disconnect_pauses_and_resume_reasks_as_a_new_activation(candidate: Candidate, db: Db) -> None:
    c = candidate
    first = await calibrate(c)
    await listen_to_prompt(c)
    await c.say(LONG)
    await c.wait_state("CANDIDATE_SPEAKING", timeout=15)
    await c.wait_agent_speaks(timeout=15)
    await c.wait_agent_quiet()
    await c.leave()
    paused = await c.wait_state("PAUSED", timeout=20)
    assert paused["paused_from"] == "CANDIDATE_SPEAKING"
    reason = db.one("SELECT reason FROM ati.pause_intervals WHERE session_id = %s", c.interview_id)
    assert reason == "network"
    await c.join()
    mark = time.monotonic()
    resumed = await c.resume()
    assert resumed["state"] == "QUESTION_ACTIVE"
    assert resumed["active_prompt"]["activation_id"] != first["active_prompt"]["activation_id"]
    assert resumed["active_prompt"]["text"] == first["active_prompt"]["text"]
    await listen_to_prompt(c, since=mark)  # welcome back + the same question
    await c.say(DONE)
    await c.wait_state("QUESTION_ACTIVE", *POST_INTERVIEW, timeout=25, activation_not=resumed["active_prompt"]["activation_id"])
    rows = db.rows(
        "SELECT close_reason FROM ati.question_activations WHERE session_id = %s AND item_id = 'q1' ORDER BY opened_at", c.interview_id
    )
    assert [r[0] for r in rows] == ["paused", "finalized"]
    n = db.one("SELECT cardinality(segment_ids) FROM ati.candidate_answers WHERE session_id = %s AND item_id = 'q1'", c.interview_id)
    assert n >= 2  # speech from both activations of the item forms one answer


async def test_pause_stops_audio_intake(candidate: Candidate, db: Db) -> None:
    c = candidate
    await calibrate(c)
    await listen_to_prompt(c)
    await c.say(LONG)
    await c.wait_state("CANDIDATE_SPEAKING", timeout=15)
    await c.wait_agent_speaks(timeout=15)
    await c.wait_agent_quiet()
    before = db.one("SELECT count(*) FROM ati.transcript_segments WHERE session_id = %s AND speaker = 'candidate'", c.interview_id)
    await c.pause()
    await c.wait_state("PAUSED", timeout=5)
    await asyncio.sleep(1.0)
    for _ in range(2):
        await c.say(LONG)
        await asyncio.sleep(1.0)
    after = db.one("SELECT count(*) FROM ati.transcript_segments WHERE session_id = %s AND speaker = 'candidate'", c.interview_id)
    assert after == before, "speech during a pause must not be transcribed"
    assert not c.audio.loud_since(time.monotonic() - 6)  # the interviewer stays silent while paused
    mark = time.monotonic()
    await c.resume()
    await listen_to_prompt(c, since=mark)
    await c.say(DONE)
    await c.wait_state("QUESTION_ACTIVE", timeout=25, activation_not=None)


async def test_prompt_injection_changes_nothing(candidate: Candidate, db: Db) -> None:
    c = candidate
    view = await calibrate(c)
    await listen_to_prompt(c)
    await c.say(INJECT)
    await c.wait_state("CANDIDATE_SPEAKING", timeout=15)
    await c.wait_agent_speaks(timeout=15)
    await c.wait_agent_quiet()
    await asyncio.sleep(1.0)
    now = await c.view()
    assert now["state"] == "CANDIDATE_SPEAKING"
    assert now["active_prompt"]["activation_id"] == view["active_prompt"]["activation_id"]
    assert db.one("SELECT count(*) FROM ati.candidate_answers WHERE session_id = %s", c.interview_id) == 0
    said = [
        r[0] for r in db.rows("SELECT text FROM ati.transcript_segments WHERE session_id = %s AND speaker = 'interviewer'", c.interview_id)
    ]
    assert "Let us stay with the question." in said
    await c.say(DONE)
    nxt = await c.wait_state("QUESTION_ACTIVE", timeout=25, activation_not=view["active_prompt"]["activation_id"])
    assert nxt["active_prompt"]["kind"] == "follow_up"  # the plan, not the candidate, decides what comes next


async def test_provider_failure_pauses_without_finalizing_and_resume_replaces_the_agent(candidate: Candidate, db: Db) -> None:
    c = candidate
    await calibrate(c)
    await listen_to_prompt(c)
    await c.say(LONG)
    await c.wait_state("CANDIDATE_SPEAKING", timeout=15)
    await c.wait_agent_speaks(timeout=15)
    await c.wait_agent_quiet()
    await c.say(FAIL_LLM)
    await c.wait_state("PAUSED", timeout=20)
    assert db.one("SELECT reason FROM ati.pause_intervals WHERE session_id = %s", c.interview_id) == "provider_failure"
    assert db.one("SELECT count(*) FROM ati.candidate_answers WHERE session_id = %s", c.interview_id) == 0
    mark = time.monotonic()
    resumed = await c.resume()
    await listen_to_prompt(c, since=mark)  # a replacement agent greets back and re-asks
    await c.say(DONE)
    await c.wait_state("QUESTION_ACTIVE", timeout=25, activation_not=resumed["active_prompt"]["activation_id"])
    leases = db.rows("SELECT fencing_token, end_reason FROM ati.agent_leases WHERE session_id = %s ORDER BY fencing_token", c.interview_id)
    assert len(leases) == 2 and leases[0][1] in ("released", "expired") and leases[1][1] is None
    assert db.one("SELECT count(*) FROM ati.candidate_answers WHERE session_id = %s", c.interview_id) == 1


async def test_candidate_finish_button_finalizes_through_the_agent_manifest(candidate: Candidate, db: Db) -> None:
    c = candidate
    view = await calibrate(c)
    await listen_to_prompt(c)
    await c.say(LONG)
    await c.wait_state("CANDIDATE_SPEAKING", timeout=15)
    await c.wait_agent_speaks(timeout=15)
    await c.wait_agent_quiet()
    await c.finish_answer()
    nxt = await c.wait_state("QUESTION_ACTIVE", timeout=15, activation_not=view["active_prompt"]["activation_id"])
    assert nxt["active_prompt"]["kind"] == "follow_up"
    cause, missing = db.rows(
        "SELECT finalization_cause, missing_segments FROM ati.candidate_answers WHERE session_id = %s", c.interview_id
    )[0]
    assert (cause, missing) == ("candidate_request", False)  # confirmed by the agent's manifest, not the deadline


async def test_agent_crash_is_replaced_without_duplicate_agents(candidate: Candidate, db: Db) -> None:
    c = candidate
    await calibrate(c)
    await listen_to_prompt(c)
    stack_cmd("agent-kill")
    try:
        paused = await c.wait_state("PAUSED", timeout=40)
        assert paused["paused_from"] == "QUESTION_ACTIVE"
        assert db.one("SELECT reason FROM ati.pause_intervals WHERE session_id = %s", c.interview_id) == "agent_unavailable"
    finally:
        stack_cmd("agent-start")
    mark = time.monotonic()
    resumed = await c.resume()
    await listen_to_prompt(c, since=mark)
    await c.say(LONG)
    await c.wait_state("CANDIDATE_SPEAKING", timeout=15)
    await c.wait_agent_speaks(timeout=15)
    await c.wait_agent_quiet()
    await c.say(DONE)
    await c.wait_state("QUESTION_ACTIVE", timeout=25, activation_not=resumed["active_prompt"]["activation_id"])
    room = db.one("SELECT room_name FROM ati.interview_sessions WHERE id = %s", c.interview_id)
    agents = await _agent_participants(room)
    assert len(agents) == 1, agents
    fencing = [r[0] for r in db.rows("SELECT fencing_token FROM ati.agent_leases WHERE session_id = %s ORDER BY issued_at", c.interview_id)]
    assert fencing == sorted(fencing) and len(set(fencing)) == len(fencing) >= 2


async def _agent_participants(room: str) -> list[str]:
    import os

    from livekit import api

    lk = api.LiveKitAPI(
        os.environ["ATI_E2E_LIVEKIT_API_URL"], os.environ["ATI_E2E_LIVEKIT_API_KEY"], os.environ["ATI_E2E_LIVEKIT_API_SECRET"]
    )
    try:
        res = await lk.room.list_participants(api.ListParticipantsRequest(room=room))
        return [p.identity for p in res.participants if p.kind == api.ParticipantInfo.Kind.AGENT]
    finally:
        await lk.aclose()
