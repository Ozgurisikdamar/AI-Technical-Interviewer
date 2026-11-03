"""Interview controller behaviour against fakes (ADR-011, ADR-012, ADR-014, ADR-016)."""

from __future__ import annotations

import asyncio
import json
import time
from collections.abc import AsyncIterator, Callable
from types import SimpleNamespace
from typing import Any

import pytest
from jsonschema import Draft202012Validator
from livekit.agents.llm.utils import build_strict_openai_schema

from tier1_voice_agent import controller as controller_mod
from tier1_voice_agent import prompt
from tier1_voice_agent.control_plane import ControlPlaneError
from tier1_voice_agent.controller import InterviewController, InterviewerAgent

from .conftest import CONTRACTS, SENTINEL, FakeControlPlane, FakeEngine, FakeRoom, make_ctx

Harness = Callable[..., tuple[InterviewController, FakeControlPlane, FakeEngine]]
ACT1, ACT2 = "act_0123456789abcdefAB", "act_ABCDEFGHIJKLMNOPqr"


async def settle(seconds: float = 0.02) -> None:
    await asyncio.sleep(seconds)


@pytest.fixture
async def worker() -> AsyncIterator[Callable[[InterviewController], None]]:
    tasks: list[asyncio.Task[Any]] = []

    def start(c: InterviewController) -> None:
        tasks.append(asyncio.create_task(c._segment_worker()))

    yield start
    for t in tasks:
        t.cancel()


def speak(c: InterviewController, *, start: bool) -> None:
    session = c.session
    assert session is not None
    if start:
        session.emit("user_state_changed", old_state="listening", new_state="speaking")  # type: ignore[attr-defined]
    else:
        session.emit("user_state_changed", old_state="speaking", new_state="listening")  # type: ignore[attr-defined]


def transcript(c: InterviewController, text: str, *, final: bool = True) -> None:
    c.session.emit("user_input_transcribed", transcript=text, is_final=final, item_id="item_1", language="en")  # type: ignore[union-attr]


# -- session setup ------------------------------------------------------------------------------


async def test_run_claims_and_starts_with_safe_room_settings() -> None:
    ctx = make_ctx("CALIBRATION")
    cp, engine = FakeControlPlane(ctx), FakeEngine()
    c = InterviewController(
        room=FakeRoom(),
        job_id="AJ_test",
        cp=cp,
        engine=engine,  # type: ignore[arg-type]
        session_id=ctx["session_ref"],
        nonce="n" * 43,
    )
    task = asyncio.create_task(c.run())
    for _ in range(100):
        if engine.session.started is not None and engine.spoken:
            break
        await settle(0.01)
    c.stop()
    await asyncio.wait_for(task, 5)
    assert cp.named("claim") == [(ctx["session_ref"], "n" * 43, "AJ_test", "agent-AJ_test")]
    started = engine.session.started
    assert started is not None and started["record"] is False
    opts = started["room_options"]
    assert opts.participant_identity == "candidate"
    assert opts.close_on_disconnect is False and opts.delete_room_on_close is False and opts.text_input is False
    assert engine.spoken == [("free", prompt.GREETING["en"])]
    assert cp.named("release") == [None] and cp.closed and engine.session.closed
    assert cp.named("usage") == [{"engine": "scripted", "model": "scripted", "input_audio_ms": 0, "output_audio_ms": 0}]


def test_the_model_has_exactly_one_tool_without_parameters(harness: Harness) -> None:
    c, _, _ = harness()
    agent = InterviewerAgent(instructions="x", controller=c)
    tools = agent.tools
    assert len(tools) == 1
    schema = build_strict_openai_schema(tools[0])  # type: ignore[arg-type]
    assert schema["function"]["name"] == "mark_answer_complete"
    assert schema["function"]["parameters"].get("properties", {}) == {}
    assert schema["function"]["description"] == prompt.TOOL_DESCRIPTION


# -- state handling ---------------------------------------------------------------------------


async def test_question_is_spoken_once_per_activation(harness: Harness) -> None:
    c, _, engine = harness(make_ctx("CALIBRATION"))
    await c._apply(make_ctx("QUESTION_ACTIVE", version=8, text="Explain MVCC."))
    await c._apply(make_ctx("QUESTION_ACTIVE", version=8, text="Explain MVCC."))
    await c._apply(make_ctx("CANDIDATE_SPEAKING", version=9, text="Explain MVCC."))
    assert engine.spoken == [("question", "Explain MVCC.")]
    assert c.agent is not None and "Explain MVCC." in c.agent.instructions


async def test_untrusted_text_never_reaches_instructions(harness: Harness) -> None:
    c, _, _ = harness(make_ctx("CALIBRATION"))
    ctx = make_ctx(
        "QUESTION_ACTIVE",
        version=8,
        input_mode="text",
        pending_text_inputs=[{"segment_id": "seg_0123456789abcdefAB", "activation_id": ACT1, "untrusted_text": SENTINEL}],
    )
    await c._apply(ctx)
    assert c.agent is not None and SENTINEL not in c.agent.instructions


async def test_pause_stops_intake_and_resume_reasks_as_a_new_activation(harness: Harness) -> None:
    c, _, engine = harness(make_ctx("CALIBRATION"))
    session = engine.session
    await c._apply(make_ctx("QUESTION_ACTIVE", version=8, activation=ACT1, text="Q2"))
    await c._apply(make_ctx("PAUSED", version=9))
    await c._apply(make_ctx("PAUSED", version=9))  # idempotent
    assert session.interrupts == 1 and session.input.audio_enabled == [False]
    await c._apply(make_ctx("QUESTION_ACTIVE", version=10, activation=ACT2, text="Q2"))
    assert session.input.audio_enabled == [False, True]
    assert engine.spoken == [("question", "Q2"), ("free", prompt.WELCOME_BACK["en"]), ("question", "Q2")]


async def test_resume_into_calibration_greets_again(harness: Harness) -> None:
    c, _, engine = harness(make_ctx("CALIBRATION"))
    c._previous_state = None
    await c._apply(make_ctx("CALIBRATION", version=7))
    await c._apply(make_ctx("PAUSED", version=8))
    await c._apply(make_ctx("CALIBRATION", version=9))
    assert engine.spoken == [("free", prompt.GREETING["en"])] * 2


async def test_cancellation_stops_the_agent(harness: Harness) -> None:
    c, _, engine = harness()
    await c._apply(make_ctx("CANCELLED", version=8))
    assert c._done.is_set() and engine.session.interrupts == 1


async def test_completion_speaks_the_closing_once_then_leaves(harness: Harness) -> None:
    c, _, engine = harness()
    await c._apply(make_ctx("INTERVIEW_COMPLETED", version=8, language="tr"))
    await c._apply(make_ctx("INTERVIEW_COMPLETED", version=8, language="tr"))
    assert engine.spoken[-1] == ("free", prompt.CLOSING["tr"]) and engine.spoken.count(("free", prompt.CLOSING["tr"])) == 1
    await settle(0.1)
    assert c._done.is_set()


def test_clock_offset_ignores_jitter_and_follows_drift(harness: Harness) -> None:
    c, _, _ = harness()
    base = make_ctx()
    epoch = base["timeline"]["epoch_unix_ms"]

    def at(server_ms: int) -> dict[str, Any]:
        ctx = make_ctx()
        ctx["timeline"] = {"epoch_unix_ms": epoch, "server_now_unix_ms": server_ms}
        return ctx

    now_ms = int(time.time() * 1000)
    c._update_skew(at(now_ms + 1000))
    first = c._skew_s
    c._update_skew(at(now_ms + 1030))  # 30 ms of response jitter
    assert c._skew_s == first
    c._update_skew(at(now_ms + 3000))  # two seconds of real drift
    assert c._skew_s is not None and first is not None and c._skew_s - first > 1.5


async def test_older_context_is_ignored(harness: Harness) -> None:
    c, _, engine = harness(make_ctx("QUESTION_ACTIVE", version=9))
    await c._apply(make_ctx("PAUSED", version=8))
    assert c.ctx["state"] == "QUESTION_ACTIVE" and engine.session.interrupts == 0


async def test_answer_window_opens_when_the_prompt_itself_plays(harness: Harness) -> None:
    # A neutral reply is still playing when the next prompt is queued; both play back to back.
    c, cp, engine = harness(make_ctx("CALIBRATION"))
    session = engine.session
    reply = object()
    session.current_speech = reply
    session.emit("agent_state_changed", old_state="listening", new_state="speaking")
    await c._apply(make_ctx("QUESTION_ACTIVE", version=8, activation=ACT1))
    t_reply = time.time()
    session.output.audio.emit("playback_started", created_at=t_reply)  # the reply, not the prompt
    await settle()
    assert cp.named("prompt_started") == []
    session.current_speech = engine.handles[-1]
    t_prompt = t_reply + 2.5
    session.output.audio.emit("playback_started", created_at=t_prompt)
    session.output.audio.emit("playback_started", created_at=t_prompt + 1)  # reported once
    await settle()
    [(act, at_ms)] = cp.named("prompt_started")
    assert act == ACT1 and at_ms == c._session_ms(t_prompt)


async def test_only_the_prompt_delivery_is_marked_for_comparison(harness: Harness, worker: Callable[[InterviewController], None]) -> None:
    c, cp, engine = harness(make_ctx("CALIBRATION"))
    worker(c)
    session = engine.session
    await c._apply(make_ctx("QUESTION_ACTIVE", version=8, activation=ACT1, text="Explain MVCC."))
    t_reply, t_prompt = time.time() - 4.0, time.time() - 2.0
    session.current_speech = engine.handles[-1]
    session.output.audio.emit("playback_started", created_at=t_prompt)
    for text, started in (("Please go on.", t_reply), ("Explain MVCC.", t_prompt), ("Take your time.", t_prompt + 3)):
        item = SimpleNamespace(
            role="assistant",
            text_content=text,
            interrupted=False,
            id=f"it_{text[:4]}",
            metrics={"started_speaking_at": started, "stopped_speaking_at": started + 1},
        )
        session.emit("conversation_item_added", item=item)
    await settle(0.05)
    marked = {s["text"]: s["prompt_activation_id"] for s in cp.segments}
    assert marked == {"Please go on.": None, "Explain MVCC.": ACT1, "Take your time.": None}


async def test_answer_window_falls_back_to_the_prompt_item_timing(harness: Harness) -> None:
    c, cp, engine = harness(make_ctx("CALIBRATION"))
    await c._apply(make_ctx("QUESTION_ACTIVE", version=8, activation=ACT1))
    started = time.time() - 1.0
    engine.handles[-1].finish(started_at=started)
    await settle()
    assert cp.named("prompt_started") == [(ACT1, c._session_ms(started))]


async def test_a_prompt_that_never_played_reports_no_window(harness: Harness) -> None:
    c, cp, engine = harness(make_ctx("CALIBRATION"))
    await c._apply(make_ctx("QUESTION_ACTIVE", version=8, activation=ACT1))
    engine.handles[-1].finish(started_at=None)
    engine.session.current_speech = engine.handles[-1]
    engine.session.output.audio.emit("playback_started", created_at=time.time())
    await settle()
    assert cp.named("prompt_started") == []


async def test_interviewer_segments_use_the_items_own_playout_timing(
    harness: Harness, worker: Callable[[InterviewController], None]
) -> None:
    c, cp, _ = harness(make_ctx("QUESTION_ACTIVE"))
    worker(c)
    t0 = time.time() - 3.0
    c.session.emit("agent_state_changed", old_state="listening", new_state="speaking")  # type: ignore[union-attr]
    item = SimpleNamespace(
        role="assistant",
        text_content="Please go on.",
        interrupted=False,
        id="it_9",
        metrics={"started_speaking_at": t0, "stopped_speaking_at": t0 + 1.5},
    )
    c.session.emit("conversation_item_added", item=item)  # type: ignore[union-attr]
    await settle(0.05)
    [seg] = cp.segments
    assert seg["start_ms"] == c._session_ms(t0) and seg["end_ms"] == c._session_ms(t0 + 1.5)


async def test_first_calibration_transcript_completes_calibration(harness: Harness, worker: Callable[[InterviewController], None]) -> None:
    c, cp, _ = harness(make_ctx("CALIBRATION"))
    worker(c)
    speak(c, start=True)
    speak(c, start=False)
    transcript(c, "hello, can you hear me")
    transcript(c, "again")
    await settle(0.1)
    assert cp.named("calibration_complete") == [7]
    assert [s["speaker"] for s in cp.segments] == ["candidate", "candidate"]


# -- completion protocol --------------------------------------------------------------------------


async def test_tool_refuses_before_the_candidate_answers(harness: Harness) -> None:
    c, cp, _ = harness(make_ctx("QUESTION_ACTIVE"))
    result = await c.on_tool_mark_answer_complete()
    assert result.startswith("Not possible now") and not cp.named("answer_complete")


async def test_tool_decides_on_the_servers_current_state(harness: Harness, worker: Callable[[InterviewController], None]) -> None:
    # The first words after a resume are "that is all": the agent has not yet seen CANDIDATE_SPEAKING.
    c, cp, _ = harness(make_ctx("QUESTION_ACTIVE", version=20))
    worker(c)
    speak(c, start=True)
    speak(c, start=False)
    transcript(c, "that is all")
    cp.server_ctx = make_ctx("CANDIDATE_SPEAKING", version=21)
    result = await c.on_tool_mark_answer_complete()
    assert result.startswith("The answer has been recorded")
    [(version, activation, keys, cause)] = cp.named("answer_complete")
    assert (version, activation, cause) == (21, ACT1, "agent_tool") and len(keys) == 1


async def test_tool_refuses_while_candidate_audio_is_degraded(harness: Harness) -> None:
    c, cp, _ = harness(make_ctx("CANDIDATE_SPEAKING"))
    c._wire_room()
    room: FakeRoom = c.room  # type: ignore[assignment]
    room.emit("track_unpublished", object(), SimpleNamespace(identity="candidate"))
    assert (await c.on_tool_mark_answer_complete()).startswith("The candidate's audio is unavailable")
    room.emit("track_subscribed", object(), object(), SimpleNamespace(identity="candidate"))
    assert c._audio_degraded is False
    assert not cp.named("answer_complete")


async def test_tool_completes_after_grace_with_the_flushed_manifest(
    harness: Harness, worker: Callable[[InterviewController], None]
) -> None:
    c, cp, _ = harness(make_ctx("CANDIDATE_SPEAKING", version=11))
    worker(c)
    speak(c, start=True)
    speak(c, start=False)
    transcript(c, "my answer")
    cp.after_answer = make_ctx("QUESTION_ACTIVE", version=13, activation=ACT2, text="Next")
    result = await c.on_tool_mark_answer_complete()
    assert result.startswith("The answer has been recorded")
    [(version, activation, keys, cause)] = cp.named("answer_complete")
    assert (version, activation, cause) == (11, ACT1, "agent_tool")
    assert keys == [s["segment_key"] for s in cp.segments] and len(keys) == 1


async def test_tool_waits_while_the_candidate_keeps_speaking(harness: Harness) -> None:
    c, cp, _ = harness(make_ctx("CANDIDATE_SPEAKING"))
    speak(c, start=True)
    result = await c.on_tool_mark_answer_complete()
    assert result.startswith("The candidate is still speaking") and not cp.named("answer_complete")


async def test_completion_waits_for_the_transcript_of_ended_speech(harness: Harness, worker: Callable[[InterviewController], None]) -> None:
    c, cp, _ = harness(make_ctx("CANDIDATE_SPEAKING"))
    worker(c)
    speak(c, start=True)
    speak(c, start=False)

    async def late() -> None:
        await settle(0.12)
        transcript(c, "last words")

    late_task = asyncio.create_task(late())
    await c.on_tool_mark_answer_complete()
    await late_task
    [(_, _, keys, _)] = cp.named("answer_complete")
    assert len(keys) == 1 and cp.segments[0]["text"] == "last words"


async def test_a_transcript_after_the_wait_keeps_its_speech_timing(harness: Harness, worker: Callable[[InterviewController], None]) -> None:
    c, cp, _ = harness(make_ctx("CANDIDATE_SPEAKING"))
    worker(c)
    speak(c, start=True)
    await settle(0.05)
    speak(c, start=False)
    spoke_at = c._session_ms()
    await c.on_tool_mark_answer_complete()
    [(_, _, keys, _)] = cp.named("answer_complete")
    assert keys == []  # nothing arrived within the wait: the server flags the gap
    await settle(0.2)
    transcript(c, "very late words")
    await settle(0.05)
    seg = cp.segments[-1]
    assert seg["end_ms"] <= spoke_at + 5 and seg["start_ms"] < seg["end_ms"]  # anchored at the speech, not at arrival


async def test_a_transcript_without_any_speech_burst_is_anchored_at_the_last_speech(
    harness: Harness, worker: Callable[[InterviewController], None]
) -> None:
    c, cp, _ = harness(make_ctx("CANDIDATE_SPEAKING"))
    worker(c)
    speak(c, start=True)
    speak(c, start=False)
    spoke_at = c._session_ms()
    transcript(c, "first")
    await settle(0.15)
    transcript(c, "second, with no speech burst of its own")  # e.g. a provider re-segmentation
    await settle(0.05)
    second = cp.segments[-1]
    assert second["start_ms"] == second["end_ms"] and abs(second["end_ms"] - spoke_at) <= 5


def timed(cp: FakeControlPlane) -> list[float]:
    stamps: list[float] = []
    original = cp.answer_complete

    async def wrapper(*args: Any) -> dict[str, Any]:
        stamps.append(time.time())
        return await original(*args)

    cp.answer_complete = wrapper  # type: ignore[method-assign]
    return stamps


async def test_grace_period_precedes_completion(harness: Harness, worker: Callable[[InterviewController], None]) -> None:
    c, cp, _ = harness(make_ctx("CANDIDATE_SPEAKING"))
    worker(c)
    stamps = timed(cp)
    speak(c, start=True)
    speak(c, start=False)
    ended = time.time()
    transcript(c, "answer")
    await c.on_tool_mark_answer_complete()
    assert stamps and stamps[0] - ended >= controller_mod.GRACE_S


async def test_speech_resuming_during_grace_delays_completion(harness: Harness, worker: Callable[[InterviewController], None]) -> None:
    c, cp, _ = harness(make_ctx("CANDIDATE_SPEAKING"))
    worker(c)
    stamps = timed(cp)
    speak(c, start=True)
    speak(c, start=False)
    transcript(c, "I think that is all")

    async def continues() -> float:
        await settle(0.02)
        speak(c, start=True)
        await settle(0.1)
        speak(c, start=False)
        transcript(c, "actually, one more thing")
        return time.time()

    resumed = asyncio.create_task(continues())
    result = await c.on_tool_mark_answer_complete()
    second_end = await resumed
    assert result.startswith("The answer has been recorded")
    assert stamps[0] - second_end >= controller_mod.GRACE_S
    [(_, _, keys, _)] = cp.named("answer_complete")
    assert len(keys) == 2


async def test_refused_segments_leave_the_manifest_transient_failures_stay(
    harness: Harness, worker: Callable[[InterviewController], None]
) -> None:
    c, cp, _ = harness(make_ctx("CANDIDATE_SPEAKING"))
    worker(c)
    cp.segment_errors = {
        "AJ_test:c:000001": ControlPlaneError(409, "segment_in_pause"),
        "AJ_test:c:000002": ControlPlaneError(0, "unreachable"),
    }
    for text in ("one", "two", "three"):
        speak(c, start=True)
        speak(c, start=False)
        transcript(c, text)
    await settle(0.05)
    assert c._activation_keys == ["AJ_test:c:000002", "AJ_test:c:000003"]


async def test_candidate_request_is_confirmed_once(harness: Harness, worker: Callable[[InterviewController], None]) -> None:
    c, cp, _ = harness(make_ctx("CANDIDATE_SPEAKING", version=12))
    worker(c)
    speak(c, start=True)
    speak(c, start=False)
    transcript(c, "done now")
    await settle(0.05)
    pending = {"activation_id": ACT1, "requested_by": "candidate", "deadline_unix_ms": 1793700130000}
    ctx = make_ctx("CANDIDATE_SPEAKING", version=12, pending_completion_request=pending)
    c._spoken_activation = ACT1
    await c._apply(ctx)
    await c._apply(ctx)
    await settle(0.1)
    calls = cp.named("answer_complete")
    assert len(calls) == 1 and calls[0][3] == "candidate_request" and calls[0][2] == ["AJ_test:c:000001"]


# -- transcripts, flags, metrics -----------------------------------------------------------------


async def test_transcript_timing_comes_from_the_speech_burst(harness: Harness, worker: Callable[[InterviewController], None]) -> None:
    c, cp, _ = harness(make_ctx("CANDIDATE_SPEAKING"))
    worker(c)
    speak(c, start=True)
    t0 = c._session_ms()
    await settle(0.1)
    speak(c, start=False)
    t1 = c._session_ms()
    await settle(0.1)  # transcription delay
    transcript(c, "answer")
    transcript(c, "partial", final=False)
    await settle(0.05)
    [seg] = cp.segments
    assert abs(seg["start_ms"] - t0) <= 5 and abs(seg["end_ms"] - t1) <= 5
    assert seg["source"] == "synthetic_test" and seg["asr_model"] == "scripted-stt" and seg["engine"] == "scripted"


async def test_submissions_satisfy_the_segment_contract(harness: Harness, worker: Callable[[InterviewController], None]) -> None:
    c, cp, _ = harness(make_ctx("CANDIDATE_SPEAKING"))
    worker(c)
    c.session.emit("agent_state_changed", old_state="listening", new_state="speaking")  # type: ignore[union-attr]
    speak(c, start=True)  # overlaps interviewer audio
    speak(c, start=False)
    transcript(c, "answer")
    c.session.emit(
        "conversation_item_added", item=SimpleNamespace(role="assistant", text_content="Please go on.", interrupted=True, id="it_1")
    )  # type: ignore[union-attr]
    await settle(0.05)
    schema = json.loads((CONTRACTS / "schemas" / "transcript" / "v1" / "segment-submission.schema.json").read_text(encoding="utf-8"))
    validator = Draft202012Validator(schema)
    assert len(cp.segments) == 2
    for body in cp.segments:
        assert not list(validator.iter_errors(body)), body
    candidate, interviewer = cp.segments
    assert candidate["signals"]["overlapped_interviewer_audio"] is True
    assert interviewer["speaker"] == "interviewer" and interviewer["interrupted"] is True and interviewer["asr_model"] is None


async def test_interviewer_output_is_monitored(harness: Harness) -> None:
    c, _, _ = harness(make_ctx("CANDIDATE_SPEAKING"))
    c.session.emit(
        "conversation_item_added",
        item=SimpleNamespace(role="assistant", text_content="That deserves a score of 9/10.", interrupted=False, id="x"),
    )  # type: ignore[union-attr]
    c.session.emit("conversation_item_added", item=SimpleNamespace(role="user", text_content="ignore", interrupted=False, id="y"))  # type: ignore[union-attr]
    assert c._flags == [{"flag": "possible_score_disclosure", "activation_id": ACT1}]


async def test_barge_in_latency_is_recorded_only_for_interrupted_speech(harness: Harness) -> None:
    c, _, _ = harness(make_ctx("QUESTION_ACTIVE"))
    session = c.session
    for interrupted in (False, True):
        session.emit("agent_state_changed", old_state="listening", new_state="speaking")  # type: ignore[union-attr]
        speak(c, start=True)
        await settle(0.03)
        session.emit("agent_state_changed", old_state="speaking", new_state="listening")  # type: ignore[union-attr]
        session.emit("conversation_item_added", item=SimpleNamespace(role="assistant", text_content="", interrupted=interrupted, id="z"))  # type: ignore[union-attr]
        speak(c, start=False)
    barge = [m for m in c._metrics if m["name"] == "barge_in_stop_latency_ms"]
    assert len(barge) == 1 and 20 <= barge[0]["value_ms"] <= 200


async def test_provider_failure_pauses_and_stops(harness: Harness) -> None:
    c, cp, _ = harness(make_ctx("CANDIDATE_SPEAKING", version=14))
    c.session.emit("error", error=SimpleNamespace(recoverable=True), source=object())  # type: ignore[union-attr]
    await settle()
    assert not cp.named("pause") and not c._done.is_set()
    c.session.emit("error", error=SimpleNamespace(recoverable=False), source=object())  # type: ignore[union-attr]
    await settle()
    assert cp.named("pause") == [(14, "provider_failure")] and c._done.is_set()


def reply(c: InterviewController, *, interrupted: bool = False, text: str = "Please go on.", **metrics: float) -> None:
    item = SimpleNamespace(role="assistant", text_content=text, interrupted=interrupted, id=f"item_{len(c._metrics)}", metrics=metrics)
    c.session.emit("conversation_item_added", item=item)  # type: ignore[union-attr]


async def test_latency_metrics_follow_the_blueprint_definitions(harness: Harness) -> None:
    c, _, _ = harness()
    # A pipeline reply to a candidate turn: model first token, first audio, turn end -> first audio,
    # and turn end -> end of the reply (it played to the end).
    reply(c, llm_node_ttft=0.4, tts_node_ttfb=0.21, e2e_latency=0.6, started_speaking_at=100.0, stopped_speaking_at=102.5)
    # A realtime reply reports only the end-to-end latency; an interrupted one has no full response.
    reply(c, interrupted=True, e2e_latency=0.7, started_speaking_at=110.0, stopped_speaking_at=110.4)
    # A prompt that follows no candidate turn has no end-of-turn latency.
    reply(c, tts_node_ttfb=0.18, started_speaking_at=120.0, stopped_speaking_at=125.0)
    names = [(m["name"], m["value_ms"]) for m in c._metrics]
    assert names == [
        ("voice_ttfb_ms", 400.0),
        ("voice_ttfa_ms", 210.0),
        ("voice_end_of_turn_ms", 600.0),
        ("voice_full_response_ms", 3100.0),
        ("voice_end_of_turn_ms", 700.0),
        ("voice_ttfa_ms", 180.0),
    ]
    reply(c, e2e_latency=-0.1)  # out of range values are dropped, not reported
    assert len(c._metrics) == 6


async def test_each_question_applies_its_turn_profile(harness: Harness) -> None:
    c, _, engine = harness(make_ctx("QUESTION_ACTIVE", activation=ACT1))
    await c._apply(make_ctx("QUESTION_ACTIVE", version=8, activation=ACT2))
    await c._apply(make_ctx("CANDIDATE_SPEAKING", version=9, activation=ACT2))  # same question: unchanged
    third = make_ctx("QUESTION_ACTIVE", version=10, activation="act_ZZZZZZZZZZZZZZZZzz")
    third["active_prompt"]["turn_profile"] = "standard"
    await c._apply(third)
    assert engine.turn_profiles == ["extended_pause", "standard"]


def tone_frame(amplitude: int, n: int = 320) -> Any:
    import numpy as np
    from livekit import rtc

    wave = (np.sin(2 * np.pi * 300 * np.arange(n) / 16000) * amplitude).astype(np.int16)
    return rtc.AudioFrame(data=wave.tobytes(), sample_rate=16000, num_channels=1, samples_per_channel=n)


def test_level_meter_flags_only_measured_quiet_turns() -> None:
    now = [100.0]
    meter = controller_mod.LevelMeter(lambda: now[0])
    for i in range(50):  # 1 s of speech at about -15 dBFS
        meter.add(tone_frame(8000), at=100.0 + i * 0.02)
    for i in range(50):  # 1 s at about -39 dBFS
        meter.add(tone_frame(500), at=102.0 + i * 0.02)
    assert meter.low_energy(100.0, 101.0) is False
    assert meter.low_energy(102.0, 103.0) is True
    assert meter.low_energy(105.0, 106.0) is False  # nothing received: never claimed
    # Trailing silence after quiet speech does not change the verdict; a loud tail does.
    assert meter.low_energy(101.5, 103.0) is True
    assert meter.low_energy(100.9, 102.5) is False


async def test_quiet_candidate_speech_is_marked_low_energy(harness: Harness, worker: Callable[[InterviewController], None]) -> None:
    c, cp, _ = harness(make_ctx("CANDIDATE_SPEAKING"))
    worker(c)
    frames = [SimpleNamespace(frame=tone_frame(400)) for _ in range(40)] + [SimpleNamespace(frame=tone_frame(9000)) for _ in range(40)]
    fed = asyncio.Event()

    class Stream:
        def __init__(self, events: list[Any]) -> None:
            self.events, self.closed = events, False

        def __aiter__(self) -> Stream:
            return self

        async def __anext__(self) -> Any:
            if not self.events:
                fed.set()
                await asyncio.sleep(3600)
            ev = self.events.pop(0)
            await asyncio.sleep(0.002)
            return ev

        async def aclose(self) -> None:
            self.closed = True

    stream = Stream(frames[:40])
    c._open_audio_stream = lambda track: stream  # type: ignore[method-assign]
    from livekit import rtc

    room: FakeRoom = c.room  # type: ignore[assignment]
    c._wire_room()
    candidate = SimpleNamespace(identity="candidate")
    speak(c, start=True)
    room.emit("track_subscribed", SimpleNamespace(kind=rtc.TrackKind.KIND_AUDIO), object(), candidate)
    await asyncio.wait_for(fed.wait(), 2)
    speak(c, start=False)
    transcript(c, "a quiet answer")
    await settle(0.05)
    assert cp.segments[-1]["signals"]["low_energy"] is True
    # The microphone goes away: metering stops and the stream is closed.
    room.emit("track_unsubscribed", SimpleNamespace(kind=rtc.TrackKind.KIND_AUDIO), object(), candidate)
    await settle(0.05)
    assert stream.closed and c._meter_task is None
    # Other participants' tracks are never metered.
    room.emit("track_subscribed", SimpleNamespace(kind=rtc.TrackKind.KIND_AUDIO), object(), SimpleNamespace(identity="agent-other"))
    assert c._meter_task is None


async def test_an_errored_voice_session_close_pauses_the_interview(harness: Harness) -> None:
    c, cp, _ = harness(make_ctx("CANDIDATE_SPEAKING", version=31))
    c.session.emit("close", reason="error", error=SimpleNamespace(recoverable=False))  # type: ignore[union-attr]
    c.session.emit("error", error=SimpleNamespace(recoverable=False), source=object())  # type: ignore[union-attr]
    await settle()
    assert cp.named("pause") == [(31, "provider_failure")] and c._done.is_set()  # paused once


async def test_a_normal_voice_session_close_stops_without_pausing(harness: Harness) -> None:
    c, cp, _ = harness(make_ctx("CANDIDATE_SPEAKING"))
    c.session.emit("close", reason="job_shutdown", error=None)  # type: ignore[union-attr]
    await settle()
    assert not cp.named("pause") and c._done.is_set()


async def test_our_own_shutdown_close_is_ignored(harness: Harness) -> None:
    c, cp, _ = harness(make_ctx("CANDIDATE_SPEAKING"))
    c.stop()
    c.session.emit("close", reason="error", error=SimpleNamespace(recoverable=False))  # type: ignore[union-attr]
    await settle()
    assert not cp.named("pause")


async def test_an_undelivered_prompt_pauses_the_interview(harness: Harness) -> None:
    c, cp, engine = harness(make_ctx("QUESTION_ACTIVE", version=40, activation=ACT1))
    await c._apply(make_ctx("QUESTION_ACTIVE", version=41, activation=ACT2))
    handle = engine.handles[-1]
    handle.exception = lambda: TimeoutError("generate_reply timed out")  # type: ignore[attr-defined]
    handle.finish(started_at=None)
    await settle()
    assert cp.named("pause") == [(41, "provider_failure")] and c._done.is_set()
    assert not cp.named("prompt_started")


async def test_text_answers_reach_the_model_once_as_user_input(harness: Harness) -> None:
    c, _, engine = harness(make_ctx("CANDIDATE_SPEAKING", version=20, input_mode="text"))
    inputs = [
        {"segment_id": "seg_0123456789abcdefAB", "activation_id": ACT1, "untrusted_text": f"{SENTINEL} first"},
        {"segment_id": "seg_ABCDEFGHIJKLMNOPqr", "activation_id": ACT1, "untrusted_text": "second"},
    ]
    await c._apply(make_ctx("CANDIDATE_SPEAKING", version=21, input_mode="text", pending_text_inputs=inputs[:1]))
    await c._apply(make_ctx("CANDIDATE_SPEAKING", version=21, input_mode="text", pending_text_inputs=inputs))
    await c._apply(make_ctx("CANDIDATE_SPEAKING", version=22, input_mode="text", pending_text_inputs=inputs))
    assert engine.user_inputs == [f"{SENTINEL} first", "second"]
    assert c.agent is not None and SENTINEL not in c.agent.instructions


async def test_text_inputs_of_another_question_are_ignored(harness: Harness) -> None:
    c, _, engine = harness(make_ctx("CANDIDATE_SPEAKING", version=20, input_mode="text"))
    stale = [{"segment_id": "seg_0123456789abcdefAB", "activation_id": ACT2, "untrusted_text": "old"}]
    await c._apply(make_ctx("CANDIDATE_SPEAKING", version=21, input_mode="text", pending_text_inputs=stale))
    assert engine.user_inputs == []


async def test_text_mode_greeting_points_to_the_begin_button(harness: Harness) -> None:
    c, _, engine = harness(make_ctx("CALIBRATION", input_mode="text"))
    c._previous_state = None
    await c._apply(make_ctx("CALIBRATION", version=7, input_mode="text", language="tr"))
    assert engine.spoken == [("free", prompt.GREETING_TEXT["tr"])]
