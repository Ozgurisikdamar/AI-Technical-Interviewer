"""Engine-agnostic interview controller (ADR-011, ADR-012, ADR-014).

The control plane owns all interview state. This controller only:
- claims the session and keeps the lease alive (fenced writes, ADR-016);
- delivers approved prompts when the flow engine activates them;
- forwards transcripts (ASR / interviewer output) with idempotent keys and speech timing;
- implements the completion protocol: the model's only tool requests completion after a speech-free
  grace period and a transcript flush; candidate button requests are confirmed with a manifest;
- disables audio intake while paused, and pauses the interview on provider failure.
It never scores, never decides what comes next, and never puts candidate text into instructions.
"""

from __future__ import annotations

import asyncio
import contextlib
import logging
import time
from collections import deque
from collections.abc import Callable
from dataclasses import dataclass
from typing import Any

from livekit import rtc
from livekit.agents import Agent, AgentSession, function_tool
from livekit.agents.voice.room_io import RoomOptions

from . import monitor, prompt
from .control_plane import ControlPlane, ControlPlaneError
from .engines import VoiceEngine

log = logging.getLogger("tier1.controller")

CANDIDATE = "candidate"
GRACE_S = 0.8  # speech-free period before acting on the model's completion request (uncalibrated)
GRACE_MAX_WAIT_S = 6.0
TRANSCRIPT_WAIT_S = 3.0  # how long completion waits for transcripts of speech that already ended
CLOSING_HOLD_S = 8.0  # time for the closing remark before the agent leaves
LIVE_STATES = ("CALIBRATION", "QUESTION_ACTIVE", "CANDIDATE_SPEAKING")


def room_options() -> RoomOptions:
    """Room I/O settings (ADR-011, ADR-016, ADR-018); unit-tested."""
    return RoomOptions(
        participant_identity=CANDIDATE,  # never link to anyone else
        close_on_disconnect=False,  # the control plane owns the room lifecycle
        delete_room_on_close=False,
        text_input=False,  # candidate text only via the authenticated API
    )


@dataclass
class _Turn:
    """One detected candidate speech burst; transcripts take their timing from it."""

    start: float
    end: float | None = None
    overlap: bool = False  # began while the interviewer was speaking


class InterviewerAgent(Agent):
    def __init__(self, *, instructions: str, controller: InterviewController) -> None:
        super().__init__(instructions=instructions)
        self._controller = controller

    @function_tool(name="mark_answer_complete", description=prompt.TOOL_DESCRIPTION)
    async def mark_answer_complete(self) -> str:
        return await self._controller.on_tool_mark_answer_complete()


class InterviewController:
    def __init__(
        self,
        *,
        room: rtc.Room,
        job_id: str,
        cp: ControlPlane,
        engine: VoiceEngine,
        session_id: str,
        nonce: str,
        clock: Callable[[], float] = time.time,
    ) -> None:
        self.room, self.job_id, self.cp, self.engine = room, job_id, cp, engine
        self.session_id, self.nonce, self.clock = session_id, nonce, clock
        self.session: AgentSession[None] | None = None
        self.agent: InterviewerAgent | None = None
        self.ctx: dict[str, Any] = {}
        self._skew_s = 0.0
        self._lock = asyncio.Lock()
        self._done = asyncio.Event()
        self._segments: asyncio.Queue[dict[str, Any]] = asyncio.Queue()
        self._inflight = 0
        self._flushed = asyncio.Condition()
        self._seq = 0
        self._greeted = False
        self._calibration_heard = False
        self._spoken_activation: str | None = None
        self._awaiting_prompt_start: str | None = None
        self._confirmed_requests: set[str] = set()
        self._activation_keys: list[str] = []
        self._previous_state: str | None = None
        self._closing_spoken = False
        # candidate speech tracking
        self._turns: deque[_Turn] = deque()
        self._open_turn: _Turn | None = None
        self._orphans: deque[_Turn] = deque()  # ended turns given up on by a completion wait
        self._last_turn: _Turn | None = None
        self._turn_changed = asyncio.Event()
        self._user_speaking = False
        self._user_quiet_since: float | None = self.clock()
        # interviewer speech tracking
        self._agent_speaking = False
        self._agent_speech_start: float | None = None
        self._barge_in_at: float | None = None
        self._barge_latency_ms: float | None = None
        self._audio_degraded = False
        self._metrics: list[dict[str, Any]] = []
        self._flags: list[dict[str, Any]] = []
        self._input_audio_s = 0.0
        self._output_audio_s = 0.0
        self._tasks: list[asyncio.Task[Any]] = []
        self._background: set[asyncio.Task[Any]] = set()

    # -- timeline ------------------------------------------------------------------------------
    def _session_ms(self, at: float | None = None) -> int:
        epoch = self.ctx.get("timeline", {}).get("epoch_unix_ms")
        if epoch is None:
            return 0
        t = (at if at is not None else self.clock()) + self._skew_s
        return max(0, int(t * 1000) - int(epoch))

    def _update_skew(self, ctx: dict[str, Any]) -> None:
        server_now = ctx.get("timeline", {}).get("server_now_unix_ms")
        if server_now:
            self._skew_s = server_now / 1000.0 - self.clock()

    def _spawn(self, coro: Any) -> None:
        task = asyncio.create_task(coro)
        self._background.add(task)
        task.add_done_callback(self._background.discard)

    # -- lifecycle -----------------------------------------------------------------------------
    async def run(self) -> None:
        identity = self.room.local_participant.identity
        ctx = await self.cp.claim(self.session_id, self.nonce, self.job_id, identity)
        prompt.validate_context(ctx)
        self.ctx = ctx
        self._update_skew(ctx)
        self.session = self.engine.build_session(language=ctx["language"])
        self.agent = InterviewerAgent(instructions=prompt.render_instructions(ctx), controller=self)
        self._wire_events(self.session)
        self._wire_room()
        await self.session.start(
            agent=self.agent,
            room=self.room,
            room_options=room_options(),
            record=False,  # no framework-level recording; consented recording is egress (ADR-017)
        )
        self._tasks = [
            asyncio.create_task(self._segment_worker()),
            asyncio.create_task(self._lease_loop()),
            asyncio.create_task(self._poll_loop()),
            asyncio.create_task(self._telemetry_loop()),
        ]
        await self._apply(ctx)
        await self._done.wait()
        await self._shutdown()

    async def _shutdown(self) -> None:
        for t in self._tasks:
            t.cancel()
        with contextlib.suppress(Exception):
            await self._drain_segments(limit_s=3.0)
        with contextlib.suppress(Exception):
            await self._send_telemetry()
        with contextlib.suppress(Exception):
            await self.cp.usage(
                {
                    "engine": self.engine.name,
                    "model": self.engine.model,
                    "input_audio_ms": int(self._input_audio_s * 1000),
                    "output_audio_ms": int(self._output_audio_s * 1000),
                }
            )
        with contextlib.suppress(Exception):
            await self.cp.release()
        if self.session is not None:
            with contextlib.suppress(Exception):
                await self.session.aclose()
        await self.cp.aclose()

    def stop(self) -> None:
        self._done.set()

    async def _lease_loop(self) -> None:
        interval = max(0.5, (self.cp.lease.renew_interval_ms if self.cp.lease else 5000) / 1000.0)
        while not self._done.is_set():
            await asyncio.sleep(interval)
            try:
                ctx = await self.cp.renew()
            except ControlPlaneError as e:
                if e.status in (401, 409):
                    log.warning("lease lost; stopping", extra={"code": e.code})
                    self.stop()  # fenced off: a newer agent owns the session
                    return
                continue
            await self._apply(ctx)

    async def _poll_loop(self) -> None:
        while not self._done.is_set():
            await asyncio.sleep(0.5)
            try:
                ctx = await self.cp.context()
            except ControlPlaneError as e:
                if e.status in (401, 409):
                    self.stop()
                    return
                continue
            await self._apply(ctx)

    # -- state handling (single serialized path) ------------------------------------------------
    async def _apply(self, ctx: dict[str, Any]) -> None:
        async with self._lock:
            prompt.validate_context(ctx)
            if self.ctx and ctx["state_version"] < self.ctx.get("state_version", 0):
                return  # an older response overtaken by a newer one
            self.ctx = ctx
            self._update_skew(ctx)
            state, prev = ctx["state"], self._previous_state
            self._previous_state = state
            assert self.session is not None and self.agent is not None
            lang = ctx["language"]
            if state == "CANCELLED":
                self.session.interrupt()
                self.stop()
                return
            if state == "PAUSED":
                if prev != "PAUSED":
                    self.session.interrupt()
                    self.session.input.set_audio_enabled(False)  # no audio intake while paused
                return
            if prev == "PAUSED":
                self.session.input.set_audio_enabled(True)
            if state == "CALIBRATION" and (not self._greeted or prev == "PAUSED"):
                self._greeted, self._calibration_heard = True, False
                await self.engine.speak_free(self.session, prompt.GREETING.get(lang, prompt.GREETING["en"]))
            if state == "INTERVIEW_COMPLETED":
                if not self._closing_spoken:
                    self._closing_spoken = True
                    await self.engine.speak_free(self.session, prompt.CLOSING.get(lang, prompt.CLOSING["en"]))
                    asyncio.get_running_loop().call_later(CLOSING_HOLD_S, self.stop)
                return
            if state not in LIVE_STATES:
                return
            active = ctx.get("active_prompt")
            if state in ("QUESTION_ACTIVE", "CANDIDATE_SPEAKING") and active and active["activation_id"] != self._spoken_activation:
                self._spoken_activation = active["activation_id"]
                self._activation_keys = []
                await self.agent.update_instructions(prompt.render_instructions(ctx))
                if prev == "PAUSED":
                    await self.engine.speak_free(self.session, prompt.WELCOME_BACK.get(lang, prompt.WELCOME_BACK["en"]))
                self._awaiting_prompt_start = active["activation_id"]
                await self.engine.speak_prompt(self.session, active["text"], active["kind"])
            pending = ctx.get("pending_completion_request")
            if (
                pending
                and active
                and pending["activation_id"] == active["activation_id"]
                and pending["activation_id"] not in self._confirmed_requests
            ):
                self._confirmed_requests.add(pending["activation_id"])
                self._spawn(self._confirm_candidate_request(pending["activation_id"]))

    async def _confirm_candidate_request(self, activation_id: str) -> None:
        await self._flush_for_completion()
        try:
            ctx = await self.cp.answer_complete(self.ctx["state_version"], activation_id, list(self._activation_keys), "candidate_request")
        except ControlPlaneError as e:
            # The server finalizes on its deadline with missing_segments=true (ADR-012).
            log.info("candidate request not confirmed", extra={"code": e.code})
            return
        await self._apply(ctx)

    # -- the model's only tool -----------------------------------------------------------------
    async def on_tool_mark_answer_complete(self) -> str:
        ctx = self.ctx
        active = ctx.get("active_prompt")
        if ctx.get("state") != "CANDIDATE_SPEAKING" or not active:
            return "Not possible now: the candidate has not started answering this question. Keep listening."
        if self._audio_degraded:
            return "The candidate's audio is unavailable, so the answer cannot be closed now. Wait."
        if not await self._wait_grace():
            return "The candidate is still speaking. Keep listening and do not interrupt."
        await self._flush_for_completion()
        if self._user_speaking:
            return "The candidate is still speaking. Keep listening and do not interrupt."
        try:
            new_ctx = await self.cp.answer_complete(
                ctx["state_version"], active["activation_id"], list(self._activation_keys), "agent_tool"
            )
        except ControlPlaneError as e:
            return f"The answer was not closed ({e.code}). Continue the interview normally."
        self._spawn(self._apply(new_ctx))
        return "The answer has been recorded. Say nothing else; the next approved question will be provided."

    async def _wait_grace(self) -> bool:
        """True once the candidate has been quiet for GRACE_S on received audio."""
        deadline = self.clock() + GRACE_MAX_WAIT_S
        while self.clock() < deadline:
            quiet = self._user_quiet_since
            if not self._user_speaking and quiet is not None and self.clock() - quiet >= GRACE_S:
                return True
            await asyncio.sleep(0.05)
        return False

    async def _flush_for_completion(self) -> None:
        """Wait for transcripts of speech that already ended, then for their delivery."""
        deadline = self.clock() + TRANSCRIPT_WAIT_S
        while any(t.end is not None for t in self._turns) and self.clock() < deadline:
            self._turn_changed.clear()
            with contextlib.suppress(TimeoutError):
                await asyncio.wait_for(self._turn_changed.wait(), 0.1)
        while self._turns and self._turns[0].end is not None:
            # No transcript arrived in time: a late one keeps this timing (and is flagged by the server).
            self._orphans.append(self._turns.popleft())
        await self._drain_segments()

    # -- events --------------------------------------------------------------------------------
    def _wire_events(self, session: AgentSession[None]) -> None:
        session.on("user_state_changed", self._on_user_state)
        session.on("agent_state_changed", self._on_agent_state)
        session.on("user_input_transcribed", self._on_user_transcript)
        session.on("user_transcription_timeout", self._on_transcription_timeout)
        session.on("conversation_item_added", self._on_item)
        session.on("metrics_collected", self._on_metrics)
        session.on("error", self._on_error)

    def _wire_room(self) -> None:
        def candidate(participant: Any) -> bool:
            return participant is not None and getattr(participant, "identity", None) == CANDIDATE

        def degraded(*args: Any) -> None:
            if candidate(args[-1]):
                self._audio_degraded = True

        def restored(*args: Any) -> None:
            if candidate(args[-1]):
                self._audio_degraded = False

        self.room.on("track_muted", degraded)
        self.room.on("track_unpublished", degraded)
        self.room.on("track_unmuted", restored)
        self.room.on("track_subscribed", restored)

    def _on_user_state(self, ev: Any) -> None:
        now = self.clock()
        if ev.new_state == "speaking":
            self._user_speaking = True
            turn = _Turn(start=now, overlap=self._agent_speaking)
            self._turns.append(turn)
            self._open_turn = self._last_turn = turn
            if self._agent_speaking:
                self._barge_in_at = now
        elif ev.old_state == "speaking":
            self._user_speaking, self._user_quiet_since = False, now
            ended, self._open_turn = self._open_turn, None
            if ended is not None:
                ended.end = now
                self._input_audio_s += now - ended.start
        self._turn_changed.set()

    def _take_turn(self, now: float) -> _Turn:
        for i, t in enumerate(self._turns):
            if t.end is not None:
                del self._turns[i]
                return t
        if self._orphans:
            return self._orphans.popleft()
        if self._turns:  # streaming final for speech still in progress
            open_turn = self._turns.popleft()
            return _Turn(start=open_turn.start, end=now, overlap=open_turn.overlap)
        last = self._last_turn  # no speech burst known: anchor at the last speech end, never "now"
        anchor = last.end if last is not None and last.end is not None else now
        return _Turn(start=anchor, end=anchor)

    def _on_user_transcript(self, ev: Any) -> None:
        if not ev.is_final or not ev.transcript.strip():
            return
        now = self.clock()
        turn = self._take_turn(now)
        self._turn_changed.set()
        self._enqueue(
            {
                "speaker": "candidate",
                "text": ev.transcript,
                "start": turn.start,
                "end": turn.end or now,
                "source": self.engine.transcript_source,
                "interrupted": False,
                "provider_item_id": getattr(ev, "item_id", None),
                "overlap": turn.overlap,
                "language": getattr(ev, "language", None),
            }
        )
        if self.ctx.get("state") == "CALIBRATION" and not self._calibration_heard:
            self._calibration_heard = True
            self._spawn(self._calibration_complete())

    def _on_transcription_timeout(self, ev: Any) -> None:
        started = float(getattr(ev, "vad_speech_started_at", 0.0))
        kept = deque(t for t in self._turns if t.end is None or t.start < started - 0.5)
        self._turns = kept
        self._turn_changed.set()

    async def _calibration_complete(self) -> None:
        await self._flush_for_completion()
        try:
            ctx = await self.cp.calibration_complete(self.ctx["state_version"])
        except ControlPlaneError as e:
            log.info("calibration not confirmed", extra={"code": e.code})
            self._calibration_heard = False
            return
        await self._apply(ctx)

    def _on_agent_state(self, ev: Any) -> None:
        now = self.clock()
        if ev.new_state == "speaking":
            self._agent_speaking, self._agent_speech_start = True, now
            self._barge_latency_ms = None
            if self._awaiting_prompt_start:
                act, self._awaiting_prompt_start = self._awaiting_prompt_start, None
                self._spawn(self._report_prompt_started(act, self._session_ms(now)))
        elif ev.old_state == "speaking":
            self._agent_speaking = False
            if self._agent_speech_start is not None:
                self._output_audio_s += now - self._agent_speech_start
                self._metric("voice_full_response_ms", (now - self._agent_speech_start) * 1000)
            if self._barge_in_at is not None:
                # Recorded only if the interviewer's item reports an interruption (see _on_item).
                self._barge_latency_ms, self._barge_in_at = (now - self._barge_in_at) * 1000, None

    async def _report_prompt_started(self, activation_id: str, at_ms: int) -> None:
        with contextlib.suppress(ControlPlaneError):
            await self._apply(await self.cp.prompt_started(activation_id, at_ms))

    def _on_item(self, ev: Any) -> None:
        item = ev.item
        if getattr(item, "role", None) != "assistant":
            return
        interrupted = bool(getattr(item, "interrupted", False))
        if interrupted and self._barge_latency_ms is not None:
            self._metric("barge_in_stop_latency_ms", self._barge_latency_ms)
            self._barge_latency_ms = None
        text = getattr(item, "text_content", None) or ""
        if not text.strip():
            return
        end = self.clock()
        start = self._agent_speech_start or end
        self._enqueue(
            {
                "speaker": "interviewer",
                "text": text,
                "start": start,
                "end": end,
                "source": "interviewer_output",
                "interrupted": interrupted,
                "provider_item_id": getattr(item, "id", None),
                "overlap": False,
                "language": self.ctx.get("language"),
            }
        )
        active = self.ctx.get("active_prompt") or {}
        for flag in monitor.scan(text):
            self._flags.append({"flag": flag, "activation_id": active.get("activation_id")})

    def _on_metrics(self, ev: Any) -> None:
        m = ev.metrics
        kind = type(m).__name__
        if kind == "TTSMetrics" and getattr(m, "ttfb", -1) >= 0:
            self._metric("voice_ttfa_ms", m.ttfb * 1000)
        elif kind in ("LLMMetrics", "RealtimeModelMetrics") and getattr(m, "ttft", -1) >= 0:
            self._metric("voice_ttfb_ms", m.ttft * 1000)
        elif kind == "EOUMetrics" and getattr(m, "end_of_utterance_delay", -1) >= 0:
            self._metric("voice_end_of_turn_ms", m.end_of_utterance_delay * 1000)

    def _on_error(self, ev: Any) -> None:
        if getattr(ev.error, "recoverable", True):
            return
        log.warning("provider failure; pausing interview", extra={"source": type(ev.source).__name__})
        self._spawn(self._provider_failed())

    async def _provider_failed(self) -> None:
        if self.ctx.get("state") in LIVE_STATES:
            with contextlib.suppress(ControlPlaneError):
                await self.cp.pause(self.ctx.get("state_version", 0), "provider_failure")
        self.stop()  # a fresh agent is dispatched when the interview continues

    def _metric(self, name: str, value_ms: float) -> None:
        if 0 <= value_ms <= 600000:
            self._metrics.append({"name": name, "value_ms": round(value_ms, 1)})

    async def _telemetry_loop(self) -> None:
        while not self._done.is_set():
            await asyncio.sleep(5)
            with contextlib.suppress(Exception):
                await self._send_telemetry()

    async def _send_telemetry(self) -> None:
        if not self._metrics and not self._flags:
            return
        metrics, flags = self._metrics[:200], self._flags[:50]
        self._metrics, self._flags = self._metrics[200:], self._flags[50:]
        await self.cp.telemetry(metrics, flags)

    # -- transcript forwarding -------------------------------------------------------------------
    def _enqueue(self, seg: dict[str, Any]) -> None:
        self._seq += 1
        speaker_tag = "c" if seg["speaker"] == "candidate" else "i"
        seg["key"] = f"{self.job_id}:{speaker_tag}:{self._seq:06d}"
        if seg["speaker"] == "candidate":
            self._activation_keys.append(seg["key"])
        self._inflight += 1
        self._segments.put_nowait(seg)

    def submission(self, seg: dict[str, Any]) -> dict[str, Any]:
        """The segment-submission/v1 body for a queued segment (validated in tests)."""
        start_ms = self._session_ms(seg["start"])
        return {
            "segment_key": seg["key"],
            "speaker": seg["speaker"],
            "text": seg["text"],
            "start_ms": start_ms,
            "end_ms": max(start_ms, self._session_ms(seg["end"])),
            "source": seg["source"],
            "confidence": None,
            "language": seg["language"],
            "interrupted": seg["interrupted"],
            "provider_item_id": seg["provider_item_id"],
            "asr_model": self.engine.asr_model if seg["speaker"] == "candidate" else None,
            "engine": self.engine.name,
            "signals": {"overlapped_interviewer_audio": bool(seg["overlap"]), "low_energy": False},
        }

    async def _segment_worker(self) -> None:
        while True:
            seg = await self._segments.get()
            try:
                await self.cp.submit_segment(self.submission(seg))
            except ControlPlaneError as e:
                if 400 <= e.status < 500:
                    # Refused for good (e.g. inside a pause): it is not part of any answer, so the
                    # completion manifest must not wait for it. The server records why.
                    with contextlib.suppress(ValueError):
                        self._activation_keys.remove(seg["key"])
                # Otherwise the key stays in the manifest: completion then fails closed and the
                # server finalizes on its deadline with missing_segments=true.
                log.info("segment not stored", extra={"code": e.code})
            finally:
                async with self._flushed:
                    self._inflight -= 1
                    self._flushed.notify_all()

    async def _drain_segments(self, limit_s: float = 5.0) -> None:
        async with self._flushed:
            await asyncio.wait_for(self._flushed.wait_for(lambda: self._inflight == 0), limit_s)
