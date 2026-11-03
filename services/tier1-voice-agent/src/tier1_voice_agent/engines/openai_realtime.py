"""OpenAI Realtime engine (ADR-004). NOT verified against the live provider in this repository:
it requires OPENAI_API_KEY, which is unavailable in the development environment.

Turn-detection eagerness and transcription settings are uncalibrated hypotheses (BLUEPRINT
section 6); they must be benchmarked by language, device and network before production.
"""

from __future__ import annotations

from typing import Any, Literal

from livekit.agents import AgentSession
from livekit.agents.voice import SpeechHandle
from livekit.plugins.openai.realtime import RealtimeModel
from openai.types.realtime import AudioTranscription
from openai.types.realtime.realtime_audio_input_turn_detection import SemanticVad

from ..prompt import ask_instruction
from . import MAX_TOOL_STEPS, turn_handling

Eagerness = Literal["low", "medium", "high", "auto"]


class OpenAIRealtimeEngine:
    name = "openai_realtime"
    transcript_source = "realtime_transcription"

    def __init__(self, *, api_key: str, model: str, voice: str, transcription_model: str, eagerness: Eagerness = "low") -> None:
        if not api_key:
            raise ValueError("OPENAI_API_KEY is required for the openai engine")
        self._api_key = api_key
        self.model: str | None = model
        self._voice = voice
        self.asr_model: str | None = transcription_model
        self._eagerness = eagerness

    def model_kwargs(self, language: str) -> dict[str, object]:
        """Exposed for tests: the exact provider configuration."""
        return {
            "model": self.model,
            "voice": self._voice,
            "api_key": self._api_key,
            "input_audio_transcription": AudioTranscription(model=self.asr_model, language=language),
            "turn_detection": SemanticVad(type="semantic_vad", eagerness=self._eagerness, create_response=True, interrupt_response=True),
        }

    def build_session(self, *, language: str) -> AgentSession[None]:
        # Turn detection and barge-in run in the provider session (server VAD); no local VAD and no
        # hosted LiveKit Inference model are used.
        kwargs: Any = self.model_kwargs(language)
        return AgentSession(
            llm=RealtimeModel(**kwargs),
            vad=None,
            turn_handling=turn_handling("realtime_llm", min_interruption_s=0.3),  # type: ignore[arg-type]
            max_tool_steps=MAX_TOOL_STEPS,
        )

    async def speak_prompt(self, session: AgentSession[None], text: str, kind: str) -> SpeechHandle | None:
        return session.generate_reply(instructions=ask_instruction(text, kind))

    async def speak_free(self, session: AgentSession[None], text: str) -> None:
        session.generate_reply(instructions=f"Say exactly this to the candidate, nothing more:\n«{text}»")
