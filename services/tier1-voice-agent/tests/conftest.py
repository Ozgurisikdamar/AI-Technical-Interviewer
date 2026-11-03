"""Shared fakes for agent tests. No network, no LiveKit server, no model provider."""

from __future__ import annotations

import copy
import json
from collections.abc import Callable
from pathlib import Path
from types import SimpleNamespace
from typing import Any

import pytest

from tier1_voice_agent import controller as controller_mod
from tier1_voice_agent.control_plane import ControlPlaneError, Lease
from tier1_voice_agent.controller import InterviewController, InterviewerAgent

REPO = Path(__file__).resolve().parents[3]
CONTRACTS = REPO / "contracts"
SENTINEL = "SENTINEL-CANDIDATE-WORDS-7f3a"


def load_fixture(kind: str, valid: bool, name: str) -> dict[str, Any]:
    folder = "valid" if valid else "invalid"
    data: dict[str, Any] = json.loads((CONTRACTS / "fixtures" / kind / folder / name).read_text(encoding="utf-8"))
    return data


def make_ctx(
    state: str = "QUESTION_ACTIVE",
    *,
    version: int = 7,
    activation: str | None = "act_0123456789abcdefAB",
    text: str = "How would you design an idempotent payment API?",
    kind: str = "question",
    **extra: Any,
) -> dict[str, Any]:
    ctx = load_fixture("tier1-context", True, "question_active.json")
    ctx["state"], ctx["state_version"] = state, version
    if activation is None or state not in ("QUESTION_ACTIVE", "CANDIDATE_SPEAKING"):
        ctx["active_prompt"] = None
    else:
        ctx["active_prompt"] = {
            "activation_id": activation,
            "item_ref": "q2",
            "kind": kind,
            "text": text,
            "turn_profile": "extended_pause",
            "prompt_started": True,
        }
    ctx.update(extra)
    return ctx


class FakeInput:
    def __init__(self) -> None:
        self.audio_enabled: list[bool] = []

    def set_audio_enabled(self, enabled: bool) -> None:
        self.audio_enabled.append(enabled)


class FakeEmitter:
    def __init__(self) -> None:
        self.handlers: dict[str, list[Callable[[Any], None]]] = {}

    def on(self, event: str, cb: Callable[[Any], None]) -> None:
        self.handlers.setdefault(event, []).append(cb)

    def emit(self, event: str, **fields: Any) -> None:
        for cb in self.handlers.get(event, []):
            cb(SimpleNamespace(**fields))


class FakeHandle:
    """Stands in for a livekit SpeechHandle."""

    def __init__(self, text: str) -> None:
        self.text = text
        self.chat_items: list[Any] = []
        self._callbacks: list[Callable[[Any], None]] = []

    def add_done_callback(self, cb: Callable[[Any], None]) -> None:
        self._callbacks.append(cb)

    def finish(self, started_at: float | None) -> None:
        if started_at is not None:
            self.chat_items = [SimpleNamespace(role="assistant", metrics={"started_speaking_at": started_at})]
        for cb in self._callbacks:
            cb(self)


class FakeSession:
    def __init__(self) -> None:
        self.handlers: dict[str, list[Callable[[Any], None]]] = {}
        self.input = FakeInput()
        self.output = SimpleNamespace(audio=FakeEmitter())
        self.current_speech: Any = None
        self.interrupts = 0
        self.started: dict[str, Any] | None = None
        self.closed = False

    def on(self, event: str, cb: Callable[[Any], None]) -> None:
        self.handlers.setdefault(event, []).append(cb)

    def emit(self, event: str, **fields: Any) -> None:
        for cb in self.handlers.get(event, []):
            cb(SimpleNamespace(**fields))

    def interrupt(self) -> None:
        self.interrupts += 1

    async def start(self, **kwargs: Any) -> None:
        self.started = kwargs

    async def aclose(self) -> None:
        self.closed = True


class FakeEngine:
    name = "scripted"
    model: str | None = "scripted"
    transcript_source = "synthetic_test"
    asr_model: str | None = "scripted-stt"

    def __init__(self) -> None:
        self.session = FakeSession()
        self.spoken: list[tuple[str, str]] = []  # (kind, text)
        self.handles: list[FakeHandle] = []
        self.user_inputs: list[str] = []  # text-mode answers passed to the model as user input
        self.turn_profiles: list[str] = []

    def build_session(self, *, language: str) -> Any:
        self.language = language
        return self.session

    def apply_turn_profile(self, session: Any, profile: str) -> None:
        self.turn_profiles.append(profile)

    async def speak_prompt(self, session: Any, text: str, kind: str) -> Any:
        self.spoken.append((kind, text))
        self.handles.append(FakeHandle(text))
        return self.handles[-1]

    async def speak_free(self, session: Any, text: str) -> None:
        self.spoken.append(("free", text))

    async def reply_to_text(self, session: Any, untrusted_text: str) -> None:
        self.user_inputs.append(untrusted_text)


class FakeRoom:
    def __init__(self) -> None:
        self.local_participant = SimpleNamespace(identity="agent-AJ_test")
        self.handlers: dict[str, list[Callable[..., None]]] = {}

    def on(self, event: str, cb: Callable[..., None]) -> None:
        self.handlers.setdefault(event, []).append(cb)

    def emit(self, event: str, *args: Any) -> None:
        for cb in self.handlers.get(event, []):
            cb(*args)


class FakeControlPlane:
    """Records calls; answers like the internal API (latest context)."""

    def __init__(self, ctx: dict[str, Any]) -> None:
        self.server_ctx = ctx
        self.calls: list[tuple[str, Any]] = []
        self.segments: list[dict[str, Any]] = []
        self.segment_errors: dict[str, ControlPlaneError] = {}
        self.answer_error: ControlPlaneError | None = None
        self.after_answer: dict[str, Any] | None = None  # context returned by answer_complete, if different
        self.lease: Lease | None = None
        self.closed = False

    async def claim(self, session_id: str, nonce: str, job_id: str, identity: str) -> dict[str, Any]:
        self.calls.append(("claim", (session_id, nonce, job_id, identity)))
        self.lease = Lease("lea_x", "grant", 1, 0, 60000)
        return copy.deepcopy(self.server_ctx)

    async def renew(self) -> dict[str, Any]:
        self.calls.append(("renew", None))
        return copy.deepcopy(self.server_ctx)

    async def context(self) -> dict[str, Any]:
        return copy.deepcopy(self.server_ctx)

    async def release(self) -> None:
        self.calls.append(("release", None))

    async def submit_segment(self, body: dict[str, Any]) -> dict[str, Any]:
        err = self.segment_errors.get(body["segment_key"])
        if err is not None:
            raise err
        self.segments.append(body)
        return {"segment_id": "seg_x", "sequence": len(self.segments)}

    async def prompt_started(self, activation_id: str, at_ms: int) -> dict[str, Any]:
        self.calls.append(("prompt_started", (activation_id, at_ms)))
        return copy.deepcopy(self.server_ctx)

    async def calibration_complete(self, state_version: int) -> dict[str, Any]:
        self.calls.append(("calibration_complete", state_version))
        return copy.deepcopy(self.server_ctx)

    async def answer_complete(self, state_version: int, activation_id: str, keys: list[str], cause: str) -> dict[str, Any]:
        self.calls.append(("answer_complete", (state_version, activation_id, list(keys), cause)))
        if self.answer_error is not None:
            raise self.answer_error
        return copy.deepcopy(self.after_answer or self.server_ctx)

    async def pause(self, state_version: int, reason: str) -> dict[str, Any]:
        self.calls.append(("pause", (state_version, reason)))
        return copy.deepcopy(self.server_ctx)

    async def telemetry(self, metrics: list[dict[str, Any]], flags: list[dict[str, Any]]) -> None:
        self.calls.append(("telemetry", (metrics, flags)))

    async def usage(self, report: dict[str, Any]) -> None:
        self.calls.append(("usage", report))

    async def aclose(self) -> None:
        self.closed = True

    def named(self, name: str) -> list[Any]:
        return [args for n, args in self.calls if n == name]


@pytest.fixture(autouse=True)
def fast_timing(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(controller_mod, "GRACE_S", 0.05)
    monkeypatch.setattr(controller_mod, "GRACE_MAX_WAIT_S", 0.5)
    monkeypatch.setattr(controller_mod, "TRANSCRIPT_WAIT_S", 0.3)
    monkeypatch.setattr(controller_mod, "CLOSING_HOLD_S", 0.05)


@pytest.fixture
def harness() -> Callable[..., tuple[InterviewController, FakeControlPlane, FakeEngine]]:
    """A controller wired to fakes, with session and agent set as run() would set them."""

    def make(ctx: dict[str, Any] | None = None) -> tuple[InterviewController, FakeControlPlane, FakeEngine]:
        ctx = ctx or make_ctx()
        cp, engine = FakeControlPlane(ctx), FakeEngine()
        c = InterviewController(
            room=FakeRoom(),
            job_id="AJ_test",
            cp=cp,
            engine=engine,  # type: ignore[arg-type]
            session_id=ctx["session_ref"],
            nonce="n" * 43,
        )
        c.session = engine.session  # type: ignore[assignment]
        c.agent = InterviewerAgent(instructions="x", controller=c)
        c._wire_events(c.session)
        c._wire_playback(c.session)
        c.ctx = copy.deepcopy(ctx)
        c._previous_state = ctx["state"]
        c._spoken_activation = (ctx.get("active_prompt") or {}).get("activation_id")  # as run() -> _apply() leaves it
        return c, cp, engine

    return make
