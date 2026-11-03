"""Engine settings: local-only turn handling, single tool step, scripted plugins (ADR-011)."""

from __future__ import annotations

import asyncio
from typing import Any

import numpy as np
import pytest
from livekit import rtc
from livekit.agents import APIConnectionError, llm, vad

from tier1_voice_agent.engines import LOCAL_INTERRUPTION_MODE, turn_handling
from tier1_voice_agent.engines.openai_realtime import OpenAIRealtimeEngine
from tier1_voice_agent.engines.scripted import (
    END_SILENCE_S,
    INTENT_TONES_HZ,
    SCRIPTED_TEXT,
    EnergyVAD,
    ScriptedEngine,
    ScriptedLLM,
    ToneTTS,
    classify,
)

SR = 16000
FRAME = 320  # 20 ms


def frame(loud: bool) -> rtc.AudioFrame:
    data = (np.full(FRAME, 8000 if loud else 10, dtype=np.int16)).tobytes()
    return rtc.AudioFrame(data=data, sample_rate=SR, num_channels=1, samples_per_channel=FRAME)


def openai_engine() -> OpenAIRealtimeEngine:
    return OpenAIRealtimeEngine(api_key="test-only-key", model="gpt-realtime", voice="marin", transcription_model="gpt-4o-transcribe")


@pytest.mark.parametrize("dev_mode", ["0", "1"])
async def test_sessions_never_select_hosted_inference(monkeypatch: pytest.MonkeyPatch, dev_mode: str) -> None:
    # In dev or hosted mode livekit-agents would otherwise pick the cloud turn detector and the
    # adaptive interruption detector, sending candidate audio to an unlisted processor.
    monkeypatch.setenv("LIVEKIT_DEV_MODE", dev_mode)
    for engine, expected in ((ScriptedEngine(), "vad"), (openai_engine(), "realtime_llm")):
        session = engine.build_session(language="en")
        try:
            th = session.options.turn_handling
            assert session.turn_detection == expected
            assert th["interruption"]["mode"] == LOCAL_INTERRUPTION_MODE
            assert th["preemptive_generation"]["enabled"] is False
            assert th["interruption"]["resume_false_interruption"] is False
            assert session.options.max_tool_steps == 1
        finally:
            await session.aclose()


async def test_openai_session_uses_server_vad_only() -> None:
    session = openai_engine().build_session(language="tr")
    try:
        assert session.vad is None and session.stt is None and session.tts is None
    finally:
        await session.aclose()


def test_openai_provider_configuration() -> None:
    kw: dict[str, Any] = openai_engine().model_kwargs("tr")
    assert kw["model"] == "gpt-realtime" and kw["voice"] == "marin"
    assert kw["input_audio_transcription"].model == "gpt-4o-transcribe" and kw["input_audio_transcription"].language == "tr"
    td = kw["turn_detection"]
    assert (td.type, td.eagerness, td.create_response, td.interrupt_response) == ("semantic_vad", "low", True, True)


def test_openai_engine_refuses_an_empty_key() -> None:
    with pytest.raises(ValueError):
        OpenAIRealtimeEngine(api_key="", model="m", voice="v", transcription_model="t")


def test_turn_handling_refuses_non_local_modes() -> None:
    with pytest.raises(ValueError):
        turn_handling("stt", min_interruption_s=0.2)


def tone(hz: float, seconds: float, rate: int = 24000) -> np.ndarray:
    n = int(rate * seconds)
    return (np.sin(2 * np.pi * hz * np.arange(n) / rate) * 8000).astype(np.int16)


@pytest.mark.parametrize("label", sorted(INTENT_TONES_HZ))
@pytest.mark.parametrize("seconds", [0.3, 1.0, 3.7])
def test_tone_pitch_encodes_the_scripted_intent_at_any_duration(label: str, seconds: float) -> None:
    assert classify(tone(INTENT_TONES_HZ[label], seconds), 24000) == label


def test_time_stretched_and_noisy_bursts_keep_their_intent() -> None:
    # A jitter buffer stretches audio by repeating short chunks (pitch is preserved), unlike resampling.
    rng = np.random.default_rng(7)
    burst = tone(INTENT_TONES_HZ["done"], 1.0)
    chunks = np.array_split(burst, 100)
    stretched = np.concatenate([c for i, c in enumerate(chunks) for _ in range(2 if i % 2 else 1)])  # 1.5x longer
    noisy = (stretched.astype(np.float64) + rng.normal(0, 800, stretched.size)).astype(np.int16)
    assert noisy.size > 1.4 * burst.size
    assert classify(noisy, 24000) == "done"


def test_unknown_pitch_and_the_interviewer_tone_are_not_intents() -> None:
    assert classify(tone(440.0, 1.0), 24000) == "part"
    assert classify(tone(2000.0, 1.0), 24000) == "part"
    assert classify(np.zeros(100, dtype=np.int16), 24000) == "part"


async def collect_vad(frames: list[rtc.AudioFrame], *, end: bool = True) -> list[vad.VADEvent]:
    stream = EnergyVAD().stream()
    for f in frames:
        stream.push_frame(f)
    if end:
        stream.end_input()
    events: list[vad.VADEvent] = []

    async def read() -> None:
        async for ev in stream:
            events.append(ev)

    try:
        await asyncio.wait_for(read(), 1.0)
    except TimeoutError:
        pass
    await stream.aclose()
    return [e for e in events if e.type != vad.VADEventType.INFERENCE_DONE]


async def test_energy_vad_detects_start_and_end() -> None:
    silence_frames = int(END_SILENCE_S / 0.02) + 2
    events = await collect_vad([frame(True)] * 50 + [frame(False)] * silence_frames)
    kinds = [e.type for e in events]
    assert kinds == [vad.VADEventType.START_OF_SPEECH, vad.VADEventType.END_OF_SPEECH]
    assert events[1].speech_duration == pytest.approx(1.0, abs=0.05)


async def test_missing_frames_never_end_a_turn() -> None:
    # A dropped track delivers no frames at all; only received silent frames count as silence.
    events = await collect_vad([frame(True)] * 50, end=False)
    assert [e.type for e in events] == [vad.VADEventType.START_OF_SPEECH]


async def run_llm(user_text: str) -> list[llm.ChatChunk]:
    ctx = llm.ChatContext.empty()
    ctx.add_message(role="user", content=user_text)
    chunks: list[llm.ChatChunk] = []
    async with ScriptedLLM().chat(chat_ctx=ctx) as stream:
        async for chunk in stream:
            chunks.append(chunk)
    return chunks


async def test_scripted_llm_calls_only_the_completion_tool_when_done() -> None:
    chunks = await run_llm(SCRIPTED_TEXT["done"])
    calls = [c for ch in chunks if ch.delta for c in (ch.delta.tool_calls or [])]
    assert [(c.name, c.arguments) for c in calls] == [("mark_answer_complete", "{}")]


async def test_scripted_llm_injection_emits_a_forbidden_tool_call() -> None:
    chunks = await run_llm(SCRIPTED_TEXT["inject"])
    calls = [c for ch in chunks if ch.delta for c in (ch.delta.tool_calls or [])]
    assert [c.name for c in calls] == ["set_candidate_score"]


async def test_scripted_llm_failure_injection() -> None:
    with pytest.raises(APIConnectionError):
        await run_llm(SCRIPTED_TEXT["fail_llm"])


async def test_tone_tts_duration_scales_with_text() -> None:
    frames = []
    async with ToneTTS().synthesize("x" * 50) as stream:
        async for ev in stream:
            frames.append(ev.frame)
    seconds = sum(f.samples_per_channel for f in frames) / ToneTTS.SAMPLE_RATE
    assert seconds == pytest.approx(2.0, abs=0.05)
