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


async def test_older_context_is_ignored(harness: Harness) -> None:
    c, _, engine = harness(make_ctx("QUESTION_ACTIVE", version=9))
    await c._apply(make_ctx("PAUSED", version=8))
    assert c.ctx["state"] == "QUESTION_ACTIVE" and engine.session.interrupts == 0


async def test_prompt_start_is_reported_when_the_interviewer_starts_speaking(harness: Harness) -> None:
    c, cp, _ = harness(make_ctx("CALIBRATION"))
    await c._apply(make_ctx("QUESTION_ACTIVE", version=8, activation=ACT1))
    c.session.emit("agent_state_changed", old_state="thinking", new_state="speaking")  # type: ignore[union-attr]
    c.session.emit("agent_state_changed", old_state="speaking", new_state="listening")  # type: ignore[union-attr]
    c.session.emit("agent_state_changed", old_state="listening", new_state="speaking")  # type: ignore[union-attr]
    await settle()
    reported = cp.named("prompt_started")
    assert len(reported) == 1 and reported[0][0] == ACT1 and reported[0][1] > 0


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
    cp.server_ctx = make_ctx("QUESTION_ACTIVE", version=13, activation=ACT2, text="Next")
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


async def test_metrics_map_to_the_allowed_names(harness: Harness) -> None:
    c, _, _ = harness()

    def emit(kind: str, **fields: float) -> None:
        cls = type(kind, (), {})
        m = cls()
        for k, v in fields.items():
            setattr(m, k, v)
        c.session.emit("metrics_collected", metrics=m)  # type: ignore[union-attr]

    emit("TTSMetrics", ttfb=0.21)
    emit("LLMMetrics", ttft=0.4)
    emit("RealtimeModelMetrics", ttft=0.3)
    emit("EOUMetrics", end_of_utterance_delay=0.6)
    emit("TTSMetrics", ttfb=-1.0)
    names = [(m["name"], m["value_ms"]) for m in c._metrics]
    assert names == [("voice_ttfa_ms", 210.0), ("voice_ttfb_ms", 400.0), ("voice_ttfb_ms", 300.0), ("voice_end_of_turn_ms", 600.0)]
