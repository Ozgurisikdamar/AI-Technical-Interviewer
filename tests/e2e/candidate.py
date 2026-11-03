"""Synthetic candidate: the public API plus a real WebRTC participant (livekit rtc).

The candidate speaks synthetic tone bursts whose pitch encodes intent for the scripted engine
(services/tier1-voice-agent/src/tier1_voice_agent/engines/scripted.py) and listens to the
interviewer's audio to detect when it speaks and when it stops. No real voice, no candidate data.
"""

from __future__ import annotations

import asyncio
import contextlib
import itertools
import os
import time
import uuid
from dataclasses import dataclass, field
from typing import Any

import httpx
import numpy as np
from livekit import rtc

SR = 48000
FRAME = 480  # 10 ms
SPEECH_AMPLITUDE = 9000
AGENT_LOUD = 1500.0  # agent tone RMS is ~4200; silence is ~0


@dataclass(frozen=True)
class Burst:
    seconds: float
    hz: float
    amplitude: int = SPEECH_AMPLITUDE


# Pitches match INTENT_TONES_HZ in the scripted engine.
PART = Burst(0.8, 300.0)
DONE = Burst(1.0, 500.0)
INJECT = Burst(1.2, 700.0)
FAIL_LLM = Burst(1.0, 900.0)
LONG = Burst(4.0, 1100.0)
FAIL_STT = Burst(1.0, 1300.0)
FAIL_TTS = Burst(1.0, 1500.0)
LLM_TIMEOUT = Burst(1.0, 1700.0)
LLM_DROP = Burst(1.0, 1900.0)
DISCLOSE = Burst(1.0, 2100.0)
QUIET = Burst(1.5, 300.0, amplitude=600)  # about -38 dBFS: heard, but below the low_energy level


def env(name: str) -> str:
    value = os.environ.get(name, "")
    if not value:
        raise RuntimeError(f"{name} is not set; run tools/dev/stack.sh up (make e2e does this)")
    return value


# Credentials observed during the run; the log scan asserts none of them appears in any log.
SECRETS_SEEN: list[str] = []


class ApiError(Exception):
    def __init__(self, status: int, body: Any) -> None:
        super().__init__(f"{status}: {body}")
        self.status, self.body = status, body


@dataclass
class AgentAudio:
    """Interviewer audio levels as heard by the candidate (monotonic timestamps)."""

    levels: list[tuple[float, float]] = field(default_factory=list)

    def loud_since(self, t0: float) -> bool:
        return any(ts >= t0 and rms > AGENT_LOUD for ts, rms in self.levels[-400:])

    def quiet_for(self, seconds: float) -> bool:
        now = time.monotonic()
        recent = [rms for ts, rms in self.levels[-400:] if ts >= now - seconds]
        return len(recent) >= int(seconds / 0.012) and max(recent, default=0.0) < AGENT_LOUD / 3

    def stopped_after(self, onset: float, window: float = 0.05) -> float | None:
        """First time after onset at which the agent stayed quiet for `window` seconds."""
        pts = [(ts, rms) for ts, rms in self.levels if ts >= onset]
        for i, (ts, _) in enumerate(pts):
            win = [r for t, r in pts[i:] if t < ts + window]
            if len(win) >= 3 and max(win) < AGENT_LOUD / 5:
                return ts
        return None


class Candidate:
    _keys = itertools.count(1)

    def __init__(self) -> None:
        self.origin = env("ATI_E2E_PUBLIC_ORIGIN")
        self.http = httpx.AsyncClient(base_url=env("ATI_E2E_PUBLIC_URL"), trust_env=False, timeout=10.0)
        self.interview_id = ""
        self.room: rtc.Room | None = None
        self.source: rtc.AudioSource | None = None
        self.audio = AgentAudio()
        self.speaking = False
        self.pitch = PART.hz
        self.amplitude = SPEECH_AMPLITUDE
        self.disconnected = asyncio.Event()
        self.media_token = ""
        self._tasks: list[asyncio.Task[Any]] = []

    # -- API --------------------------------------------------------------------------------------
    async def _call(self, method: str, path: str, body: Any = None, *, headers: dict[str, str] | None = None) -> Any:
        h = {"Origin": self.origin, "Sec-Fetch-Site": "same-origin"}
        if method == "POST":
            h["Idempotency-Key"] = f"e2e-{uuid.uuid4().hex}-{next(self._keys)}"
        h.update(headers or {})
        resp = await self.http.request(method, path, json=body, headers=h)
        data = resp.json() if resp.content else None
        if resp.status_code >= 400:
            raise ApiError(resp.status_code, data)
        return data

    async def create_and_join_api(
        self, staff_token: str, *, plan: str = "plan_demo_backend_en", voice: bool = True, recording: bool = False
    ) -> None:
        resp = await self.http.post(
            "/api/v1/interviews",
            json={"candidate_ref": f"ref_{uuid.uuid4().hex[:12]}", "plan_id": plan, "plan_version": 1},
            headers={"Authorization": f"Bearer {staff_token}", "Idempotency-Key": f"e2e-{uuid.uuid4().hex}"},
        )
        if resp.status_code != 201:
            raise ApiError(resp.status_code, resp.text)
        token = resp.json()["invitation_url"].split("#token=", 1)[1]
        SECRETS_SEEN.append(token)
        ex = await self._call("POST", "/api/v1/invitations/exchange", {"token": token})
        SECRETS_SEEN.extend(c.value for c in self.http.cookies.jar if c.value)
        self.interview_id = ex["interview_id"]
        await self._call(
            "POST",
            self._path("consent"),
            {
                "notice_id": "notice_default",
                "notice_version": "2026-11-draft.1",
                "decisions": [
                    {"purpose_id": "voice_interview_processing", "granted": voice},
                    {"purpose_id": "transcript_assessment_record", "granted": True},
                    {"purpose_id": "ai_assisted_evaluation_with_human_review", "granted": True},
                    {"purpose_id": "audio_recording", "granted": recording},
                ],
            },
        )
        view = await self.view()
        await self._call("POST", self._path("start"), {"state_version": view["state_version"], "input_mode": "voice" if voice else "text"})

    def _path(self, action: str) -> str:
        return f"/api/v1/interviews/{self.interview_id}/{action}"

    async def view(self) -> dict[str, Any]:
        data: dict[str, Any] = await self._call("GET", f"/api/v1/interviews/{self.interview_id}")
        return data

    async def wait_state(self, *states: str, timeout: float = 20.0, activation_not: str | None = None) -> dict[str, Any]:
        deadline = time.monotonic() + timeout
        last: dict[str, Any] = {}
        while time.monotonic() < deadline:
            last = await self.view()
            act = (last.get("active_prompt") or {}).get("activation_id")
            if last["state"] in states and (activation_not is None or act != activation_not):
                return last
            await asyncio.sleep(0.2)
        raise AssertionError(f"state {states} not reached in {timeout}s; last={last.get('state')} active={last.get('active_prompt')}")

    async def pause(self) -> dict[str, Any]:
        v = await self.view()
        data: dict[str, Any] = await self._call(
            "POST", self._path("pause"), {"state_version": v["state_version"], "reason": "candidate_request"}
        )
        return data

    async def resume(self) -> dict[str, Any]:
        v = await self.view()
        data: dict[str, Any] = await self._call("POST", self._path("resume"), {"state_version": v["state_version"]})
        return data

    async def finish_answer(self) -> dict[str, Any]:
        v = await self.view()
        data: dict[str, Any] = await self._call(
            "POST",
            self._path("answer-complete"),
            {"state_version": v["state_version"], "activation_id": v["active_prompt"]["activation_id"]},
        )
        return data

    # -- media ------------------------------------------------------------------------------------
    async def realtime_session(self) -> dict[str, Any]:
        rt: dict[str, Any] = await self._call("POST", self._path("realtime-session"))
        SECRETS_SEEN.append(rt["token"])
        return rt

    async def join(self) -> None:
        rt = await self.realtime_session()
        assert rt["identity"] == "candidate" and rt["can_publish_audio"] is True
        self.media_token = rt["token"]
        room = rtc.Room()
        self.disconnected.clear()

        @room.on("track_subscribed")
        def on_track(track: rtc.Track, pub: rtc.RemoteTrackPublication, p: rtc.RemoteParticipant) -> None:
            if track.kind == rtc.TrackKind.KIND_AUDIO:
                self._tasks.append(asyncio.ensure_future(self._listen(track)))

        @room.on("disconnected")
        def on_disconnected(*_: Any) -> None:
            self.disconnected.set()

        await room.connect(rt["livekit_url"], rt["token"])
        self.room = room
        self.source = rtc.AudioSource(SR, 1, queue_size_ms=100)
        track = rtc.LocalAudioTrack.create_audio_track("mic", self.source)
        await room.local_participant.publish_track(track, rtc.TrackPublishOptions(source=rtc.TrackSource.SOURCE_MICROPHONE))
        self._tasks.append(asyncio.ensure_future(self._mic()))

    async def _listen(self, track: rtc.Track) -> None:
        stream = rtc.AudioStream(track, sample_rate=SR, num_channels=1)
        async for ev in stream:
            s = np.frombuffer(ev.frame.data, dtype=np.int16)
            rms = float(np.sqrt(np.mean(s.astype(np.float64) ** 2))) if s.size else 0.0
            self.audio.levels.append((time.monotonic(), rms))
            if len(self.audio.levels) > 20000:
                del self.audio.levels[:10000]

    async def _mic(self) -> None:
        t = 0
        assert self.source is not None
        while True:
            if self.speaking:
                f = (np.sin(2 * np.pi * self.pitch * (np.arange(FRAME) + t) / SR) * self.amplitude).astype(np.int16)
                t += FRAME
            else:
                f = np.zeros(FRAME, np.int16)
            await self.source.capture_frame(rtc.AudioFrame(f.tobytes(), SR, 1, FRAME))

    async def say(self, burst: Burst) -> float:
        """Speak a synthetic burst; returns its monotonic onset."""
        onset = time.monotonic()
        self.pitch, self.amplitude = burst.hz, burst.amplitude
        self.speaking = True
        await asyncio.sleep(burst.seconds)
        self.speaking = False
        return onset

    async def wait_agent_speaks(self, timeout: float = 20.0, since: float | None = None) -> float:
        t0 = since if since is not None else time.monotonic()
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if self.audio.loud_since(t0):
                return time.monotonic()
            await asyncio.sleep(0.02)
        raise AssertionError("the interviewer did not speak")

    async def wait_agent_quiet(self, quiet: float = 0.8, timeout: float = 30.0) -> None:
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if self.audio.quiet_for(quiet):
                return
            await asyncio.sleep(0.05)
        raise AssertionError("the interviewer did not stop speaking")

    async def leave(self) -> None:
        for t in self._tasks:
            t.cancel()
        self._tasks = []
        if self.room is not None:
            with contextlib.suppress(Exception):
                await self.room.disconnect()
        self.room = None

    async def close(self) -> None:
        await self.leave()
        await self.http.aclose()


# -- interview steps ------------------------------------------------------------------------------


async def calibrate(c: Candidate) -> dict[str, Any]:
    """Greeting, a few words from the candidate, then the first question is activated."""
    await c.wait_agent_speaks(timeout=30)
    await c.wait_agent_quiet()
    await c.say(PART)
    return await c.wait_state("QUESTION_ACTIVE", timeout=20)


async def listen_to_prompt(c: Candidate, since: float | None = None) -> None:
    await c.wait_agent_speaks(timeout=20, since=since)
    await c.wait_agent_quiet()


async def answer(c: Candidate, *, body: Burst = LONG) -> dict[str, Any]:
    """Answer the open question: speak, wait for the neutral reply, say it is complete."""
    view = await c.view()
    act = view["active_prompt"]["activation_id"]
    await c.say(body)
    await c.wait_state("CANDIDATE_SPEAKING", timeout=15)
    await c.wait_agent_speaks(timeout=15)
    await c.wait_agent_quiet()
    mark = time.monotonic()
    await c.say(DONE)
    nxt = await c.wait_state("QUESTION_ACTIVE", "CANDIDATE_SPEAKING", "INTERVIEW_COMPLETED", timeout=25, activation_not=act)
    if nxt["state"] != "INTERVIEW_COMPLETED":
        await listen_to_prompt(c, since=mark)
    return nxt
