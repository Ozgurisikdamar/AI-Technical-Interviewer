"""Browser E2E: the web UI in Chromium against the real stack (scripted engine, loopback).

Chromium's fake microphone loops a pitch-coded WAV generated here (it is a synthetic test signal,
never candidate audio, and is deleted afterwards). The flow covers invitation, consent, device
check, the live room with pause/resume and a page refresh, and completion.
"""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import wave
from pathlib import Path
from typing import Any

import numpy as np
import pytest

from candidate import DONE, LONG, PART, SR
from conftest import ROOT, Db, stack_cmd

NODE_PATH = subprocess.run(["npm", "root", "-g"], capture_output=True, text=True).stdout.strip() if shutil.which("npm") else ""


def fake_microphone(path: Path) -> None:
    """A 24 s loop: a short calibration-style burst, a long answer burst, then "that is all"."""
    schedule = [(7.0, None), (PART.seconds, PART.hz), (6.2, None), (3.0, LONG.hz), (3.5, None), (DONE.seconds, DONE.hz), (2.5, None)]
    chunks = []
    for seconds, hz in schedule:
        n = int(SR * seconds)
        chunks.append(np.zeros(n) if hz is None else np.sin(2 * np.pi * hz * np.arange(n) / SR) * 9000)
    data = np.concatenate(chunks).astype(np.int16)
    with wave.open(str(path), "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(SR)
        w.writeframes(data.tobytes())


requires_playwright = pytest.mark.skipif(
    not NODE_PATH or not (Path(NODE_PATH) / "playwright").exists(), reason="Playwright for Node is not installed"
)


def run_flow(stack: dict[str, str], staff_token: str, script: str) -> dict[str, Any]:
    stack_cmd("web-up")
    mic = Path(stack["ATI_E2E_STATE_DIR"]) / "e2e-fake-microphone.wav"
    fake_microphone(mic)
    try:
        env = {
            **os.environ,
            "NODE_PATH": NODE_PATH,
            "ATI_E2E_STAFF_TOKEN": staff_token,
            "ATI_E2E_FAKE_MIC": str(mic),
            "NO_PROXY": "localhost,127.0.0.1",
            "no_proxy": "localhost,127.0.0.1",
        }
        out = subprocess.run(
            ["node", str(ROOT / "tests" / "e2e" / "browser" / script)], capture_output=True, text=True, env=env, timeout=600
        )
    finally:
        mic.unlink(missing_ok=True)
    lines = [line for line in out.stdout.splitlines() if line.startswith("{")]
    assert lines, out.stdout[-2000:] + out.stderr[-2000:]
    result: dict[str, Any] = json.loads(lines[-1])
    print(f"\n{script} steps:", ", ".join(s["name"] for s in result["steps"]))
    assert result["ok"], result
    return result


@requires_playwright
def test_voice_interview_in_the_browser(stack: dict[str, str], staff_token: str, db: Db) -> None:
    sid = run_flow(stack, staff_token, "voice_flow.mjs")["interview_id"]
    assert db.one("SELECT state FROM ati.interview_sessions WHERE id = %s", sid) == "INTERVIEW_COMPLETED"
    items = [r[0] for r in db.rows("SELECT item_id FROM ati.candidate_answers WHERE session_id = %s ORDER BY finalized_at", sid)]
    assert items == ["q1", "q1f1", "q2", "q3"]
    reasons = {r[0] for r in db.rows("SELECT reason FROM ati.pause_intervals WHERE session_id = %s", sid)}
    assert "candidate_request" in reasons  # the pause button
    assert db.one("SELECT count(*) FROM ati.consent_records WHERE session_id = %s", sid) >= 4


@requires_playwright
def test_keyboard_only_with_device_loss_and_withdrawal(stack: dict[str, str], staff_token: str, db: Db) -> None:
    sid = run_flow(stack, staff_token, "keyboard_flow.mjs")["interview_id"]
    state, reason = db.rows("SELECT state, cancel_reason FROM ati.interview_sessions WHERE id = %s", sid)[0]
    assert (state, reason) == ("CANCELLED", "withdrawn")
    reasons = {r[0] for r in db.rows("SELECT reason FROM ati.pause_intervals WHERE session_id = %s", sid)}
    assert "device_lost" in reasons
    assert db.one("SELECT count(*) FROM ati.outbox_events WHERE session_id = %s AND event_type = 'interview.completed'", sid) == 0


@requires_playwright
def test_text_mode_when_the_microphone_is_refused(stack: dict[str, str], staff_token: str, db: Db) -> None:
    sid = run_flow(stack, staff_token, "text_flow.mjs")["interview_id"]
    state, mode = db.rows("SELECT state, input_mode FROM ati.interview_sessions WHERE id = %s", sid)[0]
    assert (state, mode) == ("INTERVIEW_COMPLETED", "text")
    answers = db.rows(
        "SELECT item_id, finalization_cause, segment_ids FROM ati.candidate_answers WHERE session_id = %s ORDER BY finalized_at", sid
    )
    assert [a[0] for a in answers] == ["q1", "q1f1", "q2", "q3"]
    assert all(a[1] == "candidate_request" for a in answers)
    sources = {
        r[0] for r in db.rows("SELECT DISTINCT source FROM ati.transcript_segments WHERE session_id = %s AND speaker = 'candidate'", sid)
    }
    assert sources == {"candidate_text"}  # no speech was transcribed: nothing was published
    assert db.one("SELECT count(*) FROM ati.audio_recordings WHERE session_id = %s", sid) == 0
