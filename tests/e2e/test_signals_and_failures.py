"""Injected provider failures, quiet speech, latency metrics, policy flags, per-question turn profiles
and a Turkish interview, over real WebRTC with the scripted engine (loopback only)."""

from __future__ import annotations

import asyncio
import json
import os
import re
import time
from pathlib import Path

import httpx
import pytest

from candidate import (
    DISCLOSE,
    DONE,
    FAIL_STT,
    FAIL_TTS,
    LLM_DROP,
    LLM_TIMEOUT,
    LONG,
    QUIET,
    Burst,
    Candidate,
    answer,
    calibrate,
    listen_to_prompt,
)
from conftest import Db

pytestmark = pytest.mark.asyncio(loop_scope="session")


async def into_answer(c: Candidate) -> dict[str, object]:
    view = await calibrate(c)
    await listen_to_prompt(c)
    await c.say(LONG)
    await c.wait_state("CANDIDATE_SPEAKING", timeout=15)
    await c.wait_agent_speaks(timeout=15)
    await c.wait_agent_quiet()
    return view


@pytest.mark.parametrize(
    "failure",
    [FAIL_STT, FAIL_TTS, LLM_TIMEOUT, LLM_DROP],
    ids=["speech_recognition", "speech_synthesis", "model_timeout", "stream_dropped"],
)
async def test_injected_provider_failures_pause_without_finalizing(candidate: Candidate, db: Db, failure: Burst) -> None:
    c = candidate
    await into_answer(c)
    await c.say(failure)
    paused = await c.wait_state("PAUSED", timeout=30)
    assert paused["paused_from"] == "CANDIDATE_SPEAKING"
    assert db.one("SELECT reason FROM ati.pause_intervals WHERE session_id = %s", c.interview_id) == "provider_failure"
    assert db.one("SELECT count(*) FROM ati.candidate_answers WHERE session_id = %s", c.interview_id) == 0
    mark = time.monotonic()
    resumed = await c.resume()
    await listen_to_prompt(c, since=mark)  # a replacement agent re-asks the same question
    await c.say(DONE)
    await c.wait_state("QUESTION_ACTIVE", timeout=25, activation_not=resumed["active_prompt"]["activation_id"])
    assert db.one("SELECT count(*) FROM ati.candidate_answers WHERE session_id = %s", c.interview_id) == 1


async def test_quiet_speech_is_flagged_low_energy(candidate: Candidate, db: Db) -> None:
    c = candidate
    view = await calibrate(c)
    await listen_to_prompt(c)
    await c.say(QUIET)
    await c.wait_state("CANDIDATE_SPEAKING", timeout=15)
    await c.wait_agent_speaks(timeout=15)
    await c.wait_agent_quiet()
    await c.say(DONE)
    await c.wait_state("QUESTION_ACTIVE", timeout=25, activation_not=view["active_prompt"]["activation_id"])
    flags = [
        r[0]
        for r in db.rows(
            """SELECT (flags->>'low_energy')::bool FROM ati.transcript_segments
               WHERE session_id = %s AND speaker = 'candidate' ORDER BY sequence""",
            c.interview_id,
        )
    ]
    assert flags == [False, True, False], flags  # calibration, the quiet fragment, the loud "done"
    payload = db.one("SELECT payload FROM ati.outbox_events WHERE session_id = %s AND event_type = 'answer.finalized'", c.interview_id)
    assert "low_energy" in payload["integrity_flags"]


def _metric_counts(text: str, family: str) -> dict[str, float]:
    out: dict[str, float] = {}
    for m in re.finditer(rf'^{family}_count\{{(\w+)="([^"]+)"\}} (\S+)$', text, re.M):
        out[m.group(2)] = float(m.group(3))
    return out


async def test_latency_metrics_and_policy_flags_reach_the_control_plane(candidate: Candidate, db: Db) -> None:
    c = candidate
    view = await calibrate(c)
    await listen_to_prompt(c)
    await c.say(DISCLOSE)  # the scripted model answers with a score: the output monitor must flag it
    await c.wait_state("CANDIDATE_SPEAKING", timeout=15)
    await c.wait_agent_speaks(timeout=15)
    await c.wait_agent_quiet()
    activation = view["active_prompt"]["activation_id"]
    deadline = time.monotonic() + 15  # telemetry is sent every 5 s
    flags: list[tuple[object, ...]] = []
    while time.monotonic() < deadline and not flags:
        flags = db.rows("SELECT details FROM ati.integrity_events WHERE session_id = %s AND kind = 'policy_flag'", c.interview_id)
        await asyncio.sleep(0.5)
    assert flags and flags[0][0] == {"flag": "possible_score_disclosure", "activation_id": activation}
    async with httpx.AsyncClient(trust_env=False) as http:
        text = (await http.get(os.environ["ATI_E2E_INTERNAL_URL"] + "/metrics")).text
    latency = _metric_counts(text, "ati_voice_latency_ms")
    for name in ("voice_ttfb_ms", "voice_ttfa_ms", "voice_end_of_turn_ms", "voice_full_response_ms"):
        assert latency.get(name, 0) > 0, (name, latency)
    assert re.search(r'^ati_tier1_policy_flags_total\{flag="possible_score_disclosure"\} [1-9]', text, re.M)
    assert c.interview_id not in text  # no session identifiers in metric labels


async def test_turkish_interview_uses_each_questions_turn_profile(stack: dict[str, str], staff_token: str, db: Db) -> None:
    log = Path(stack["ATI_E2E_STATE_DIR"]) / "logs" / "agent.log"
    offset = log.stat().st_size
    c = Candidate()
    try:
        await c.create_and_join_api(staff_token, plan="plan_demo_backend_tr")
        await c.join()
        view = await calibrate(c)
        assert view["active_prompt"]["text"].startswith("PostgreSQL'in")
        await listen_to_prompt(c)
        for _ in range(6):
            view = await answer(c)
            if view["state"] == "INTERVIEW_COMPLETED":
                break
        assert view["state"] == "INTERVIEW_COMPLETED", view
        await c.wait_agent_speaks(timeout=10)  # the Turkish closing remark
        await asyncio.wait_for(c.disconnected.wait(), 30)
    finally:
        await c.close()
    sid = c.interview_id
    answers = [r[0] for r in db.rows("SELECT item_id FROM ati.candidate_answers WHERE session_id = %s ORDER BY finalized_at", sid)]
    assert answers == ["q1", "q1f1", "q2", "q3"]
    assert db.one("SELECT notice_language FROM ati.interview_sessions WHERE id = %s", sid) == "tr"
    said = db.rows(
        "SELECT language, text FROM ati.transcript_segments WHERE session_id = %s AND speaker = 'interviewer' ORDER BY sequence", sid
    )
    assert said and all(lang == "tr" for lang, _ in said)
    texts = " ".join(t for _, t in said)
    assert "Merhaba" in texts and "Bu son soruydu" in texts and "aynı ödemeyi asla iki kez" in texts
    room = db.one("SELECT room_name FROM ati.interview_sessions WHERE id = %s", sid)
    with log.open(encoding="utf-8") as f:
        f.seek(offset)
        applied = [json.loads(line) for line in f if '"turn profile applied"' in line]
    profiles = {e["item_ref"]: e["turn_profile"] for e in applied if e.get("room") == room}
    assert profiles.get("q2") == "extended_pause" and profiles.get("q1") == "standard" and profiles.get("q3") == "standard", profiles
    assert set(profiles) == {"q1", "q1f1", "q2", "q3"}
