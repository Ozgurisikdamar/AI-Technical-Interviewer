"""Application-owned voice engine interface (ADR-004, ADR-011)."""

from __future__ import annotations

from typing import Any, Protocol

from livekit.agents import AgentSession
from livekit.agents.voice import SpeechHandle

# livekit-agents 1.8 can select hosted LiveKit Inference services on its own: the cloud end-of-turn
# model (default turn detector in dev or hosted mode) and the adaptive interruption detector. Both
# would send candidate audio or transcripts to a processor that the consent notice does not name,
# so every engine pins local modes explicitly and tests assert it (ADR-011, THREAT_MODEL addendum).
LOCAL_TURN_DETECTION = ("vad", "realtime_llm", "manual")
LOCAL_INTERRUPTION_MODE = "vad"


def turn_handling(turn_detection: str, *, min_interruption_s: float, min_endpointing_s: float | None = None) -> dict[str, Any]:
    if turn_detection not in LOCAL_TURN_DETECTION:
        raise ValueError(f"turn detection {turn_detection!r} is not a local mode")
    options: dict[str, Any] = {
        "turn_detection": turn_detection,
        "interruption": {
            "enabled": True,
            "mode": LOCAL_INTERRUPTION_MODE,
            "min_duration": min_interruption_s,
            # A barge-in is never "undone": the interviewer does not resume a cut-off sentence.
            "resume_false_interruption": False,
            "false_interruption_timeout": None,
        },
        # No speculative replies: the interviewer only speaks approved prompts or short neutral turns.
        "preemptive_generation": {"enabled": False},
    }
    if min_endpointing_s is not None:
        options["endpointing"] = {"mode": "fixed", "min_delay": min_endpointing_s}
    return options


# One tool call per model turn, no chaining (single-tool policy, ADR-011). livekit-agents counts
# steps from 1 and lets the reply to a tool result call tools until num_steps >= max_tool_steps + 1,
# so 1 would still allow one chained call; with 0 the reply to a tool result is generated with
# tool_choice="none" (tests/test_tool_steps.py).
MAX_TOOL_STEPS = 0

# Per-question turn profiles (tier1-context/v1 active_prompt.turn_profile). "extended_pause" gives
# the candidate longer to think before a pause counts as the end of their turn. Each engine maps a
# profile to its own turn-detection setting; the values are uncalibrated (BLUEPRINT section 6).
TURN_PROFILES = ("standard", "extended_pause")


class VoiceEngine(Protocol):
    """Builds an AgentSession and delivers approved prompts. Implementations are interchangeable;
    the interview controller never depends on a specific provider."""

    name: str  # "scripted" | "openai_realtime" (segment provenance engine)
    model: str | None  # provider model reported in usage
    transcript_source: str  # "synthetic_test" | "realtime_transcription"
    asr_model: str | None

    def build_session(self, *, language: str) -> AgentSession[None]: ...

    def apply_turn_profile(self, session: AgentSession[None], profile: str) -> None:
        """Apply the active question's turn profile; takes effect at the next end-of-turn decision."""

    async def speak_prompt(self, session: AgentSession[None], text: str, kind: str) -> SpeechHandle | None:
        """Deliver an approved prompt; the returned handle identifies its playout (answer window, ADR-014)."""

    async def speak_free(self, session: AgentSession[None], text: str) -> None: ...

    async def reply_to_text(self, session: AgentSession[None], untrusted_text: str) -> None:
        """Text mode (ADR-018): the candidate's text goes to the model as user input, never as instructions."""
