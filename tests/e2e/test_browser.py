"""Browser E2E: the web UI in Chromium against the real stack (scripted engine, loopback).

Chromium's fake microphone loops a pitch-coded WAV generated here (it is a synthetic test signal,
never candidate audio, and is deleted afterwards). The candidate flows cover invitation, consent,
device check, the live room with pause/resume and a page refresh, and completion. The review
console flow covers staff sign-in, independent review of every answer, the report and sign-out.
"""

from __future__ import annotations

import asyncio
import hashlib
import json
import os
import shutil
import subprocess
import wave
from pathlib import Path
from typing import Any

import numpy as np
import pytest

from candidate import DONE, LONG, PART, POST_INTERVIEW, SR
from conftest import ROOT, Db, release, stack_cmd
from review_data import EXPECTED_SCORES, typed_interview, wait_db_state

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


BROWSER = ROOT / "tests" / "e2e" / "browser"


def run_flow(stack: dict[str, str], staff_token: str, script: str, extra_env: dict[str, str] | None = None) -> dict[str, Any]:
    stack_cmd("web-up")
    if not (BROWSER / "node_modules" / "axe-core").exists():  # pinned in browser/package-lock.json
        subprocess.run(
            ["npm", "ci", "--ignore-scripts", "--no-audit", "--no-fund"], cwd=BROWSER, check=True, capture_output=True, timeout=300
        )
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
            **(extra_env or {}),
        }
        out = subprocess.run(["node", str(BROWSER / script)], capture_output=True, text=True, env=env, timeout=600)
    finally:
        mic.unlink(missing_ok=True)
    lines = [line for line in out.stdout.splitlines() if line.startswith("{")]
    assert lines, out.stdout[-2000:] + out.stderr[-2000:]
    result: dict[str, Any] = json.loads(lines[-1])
    print(f"\n{script} steps:", ", ".join(s["name"] for s in result["steps"]))
    if result.get("a11y"):
        print(f"{script} minor accessibility findings:", json.dumps(result["a11y"]))
    assert result["ok"], result
    return result


@requires_playwright
def test_voice_interview_in_the_browser(stack: dict[str, str], staff_token: str, db: Db) -> None:
    sid = run_flow(stack, staff_token, "voice_flow.mjs")["interview_id"]
    assert db.one("SELECT state FROM ati.interview_sessions WHERE id = %s", sid) in POST_INTERVIEW
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
    assert state in POST_INTERVIEW and mode == "text"
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


@requires_playwright
def test_declining_a_required_purpose_cancels_in_the_browser(stack: dict[str, str], staff_token: str, db: Db) -> None:
    sid = run_flow(stack, staff_token, "consent_decline_flow.mjs")["interview_id"]
    state, reason = db.rows("SELECT state, cancel_reason FROM ati.interview_sessions WHERE id = %s", sid)[0]
    assert (state, reason) == ("CANCELLED", "consent_declined")
    decisions = dict(db.rows("SELECT purpose_id, granted FROM ati.consent_records WHERE session_id = %s", sid))
    assert decisions["transcript_assessment_record"] is False and len(decisions) == 4
    assert db.one("SELECT count(*) FROM ati.outbox_events WHERE session_id = %s", sid) == 0


@requires_playwright
def test_a_refused_microphone_falls_back_to_text(stack: dict[str, str], staff_token: str, db: Db) -> None:
    sid = run_flow(stack, staff_token, "mic_denied_flow.mjs")["interview_id"]
    state, mode, reason = db.rows("SELECT state, input_mode, cancel_reason FROM ati.interview_sessions WHERE id = %s", sid)[0]
    assert (state, mode, reason) == ("CANCELLED", "text", "withdrawn")
    assert (
        db.one("SELECT granted FROM ati.consent_records WHERE session_id = %s AND purpose_id = 'voice_interview_processing'", sid) is True
    )
    answers = db.rows("SELECT item_id, finalization_cause FROM ati.candidate_answers WHERE session_id = %s", sid)
    assert answers == [("q1", "candidate_request")]
    sources = {
        r[0] for r in db.rows("SELECT DISTINCT source FROM ati.transcript_segments WHERE session_id = %s AND speaker = 'candidate'", sid)
    }
    assert sources == {"candidate_text"}


@requires_playwright
def test_no_microphone_device_falls_back_to_text(stack: dict[str, str], staff_token: str, db: Db) -> None:
    sid = run_flow(stack, staff_token, "no_device_flow.mjs")["interview_id"]
    state, mode = db.rows("SELECT state, input_mode FROM ati.interview_sessions WHERE id = %s", sid)[0]
    assert (state, mode) == ("CANCELLED", "text")


@requires_playwright
def test_leaving_the_page_pauses_and_returning_resumes(stack: dict[str, str], staff_token: str, db: Db) -> None:
    sid = run_flow(stack, staff_token, "reconnect_flow.mjs")["interview_id"]
    reasons = [r[0] for r in db.rows("SELECT reason FROM ati.pause_intervals WHERE session_id = %s ORDER BY started_ms", sid)]
    assert reasons[0] == "network", reasons
    closes = [
        r[0]
        for r in db.rows(
            "SELECT close_reason FROM ati.question_activations WHERE session_id = %s AND item_id = 'q1' ORDER BY opened_at", sid
        )
    ]
    assert closes[0] == "paused" and len(closes) >= 2  # resumed as a new activation of the same question
    assert db.one("SELECT cancel_reason FROM ati.interview_sessions WHERE id = %s", sid) == "withdrawn"


@requires_playwright
async def test_reviewer_decides_every_answer_in_the_browser(stack: dict[str, str], staff_token: str, reviewer_token: str, db: Db) -> None:
    c = await typed_interview(staff_token, db)
    sid = c.interview_id
    await release(c)
    await wait_db_state(db, sid, "HUMAN_REVIEW")
    secrets = Path(stack["ATI_E2E_STATE_DIR"]) / "browser_secrets"  # the session cookies, scanned for in the logs
    secrets.unlink(missing_ok=True)
    extra = {"ATI_E2E_REVIEWER_TOKEN": reviewer_token, "ATI_E2E_INTERVIEW_ID": sid, "ATI_E2E_SECRETS_FILE": str(secrets)}
    if os.environ.get("ATI_E2E_SCREENSHOTS"):
        extra["ATI_E2E_SCREENSHOTS"] = os.environ["ATI_E2E_SCREENSHOTS"]
    await asyncio.to_thread(run_flow, stack, staff_token, "reviewer_flow.mjs", extra)

    def token_row(token: str) -> str:
        return str(db.one("SELECT id FROM ati.staff_tokens WHERE token_hash = %s", hashlib.sha256(token.encode()).hexdigest()))

    reviewer, recruiter = token_row(reviewer_token), token_row(staff_token)

    # Every answer was decided in the browser by the reviewer, with the reviewer's own scores; the
    # AI's scores are kept as the prior, and whether the draft was opened is recorded (the third
    # answer was decided with the keyboard, without opening it).
    outcomes = db.rows(
        """SELECT a.item_id, o.reviewer_id, o.scores, o.weighted_score_centi, o.prior_scores, o.ai_draft_viewed FROM ati.review_outcomes o
           JOIN ati.candidate_answers a ON a.id = o.answer_id WHERE o.session_id = %s ORDER BY a.finalized_at""",
        sid,
    )
    mine = {"technical_accuracy": 60, "architecture_maturity": 50, "edge_case_awareness": 40, "claim_support": 30}
    assert [(r[0], r[1], r[2], r[3], r[4], r[5]) for r in outcomes] == [
        (item, reviewer, mine, 4900, EXPECTED_SCORES[item], item != "q2") for item in EXPECTED_SCORES
    ]
    assert db.one("SELECT state FROM ati.interview_sessions WHERE id = %s", sid) == "REPORT_READY"
    last = db.one("SELECT transition_id FROM ati.session_events WHERE session_id = %s ORDER BY state_version DESC LIMIT 1", sid)
    assert last == "T17"
    assert db.one("SELECT count(*) FROM ati.reports WHERE session_id = %s", sid) == 1

    # One browser session each, never longer than 8 hours, stored as the hash of the cookie; the
    # reviewer's ended at sign-out, the recruiter's is still open. Each start and end is audited.
    cookies = [line for line in secrets.read_text().splitlines() if line]
    assert len(cookies) == 2
    hashes = [hashlib.sha256(v.encode()).hexdigest() for v in cookies]
    sessions = {
        r[0]: r[1:]
        for r in db.rows(
            """SELECT token_hash, id, staff_token_id, expires_at - created_at <= interval '8 hours', revoked_at IS NOT NULL
               FROM ati.staff_sessions WHERE token_hash = ANY(%s)""",
            hashes,
        )
    }
    for cookie_hash, token_id, ended in [(hashes[0], reviewer, True), (hashes[1], recruiter, False)]:
        session_id, owner, bounded, revoked = sessions[cookie_hash]
        assert (owner, bounded, revoked) == (token_id, True, ended)
        actions = sorted(
            r[0]
            for r in db.rows(
                "SELECT action FROM ati.audit_logs WHERE subject_type = 'staff_session' AND subject_id = %s AND actor_id = %s",
                session_id,
                token_id,
            )
        )
        assert actions == (["staff.session.end", "staff.session.start"] if ended else ["staff.session.start"]), actions

    # No credential was written anywhere: not in the audit log, not in stored replays.
    for value in [reviewer_token, staff_token, *cookies]:
        assert db.one("SELECT count(*) FROM ati.audit_logs WHERE details::text LIKE %s", f"%{value}%") == 0
        assert db.one("SELECT count(*) FROM ati.idempotency_records WHERE response::text LIKE %s", f"%{value}%") == 0
    assert db.one("SELECT count(*) FROM ati.audit_logs WHERE details::text LIKE %s", "%ati_st_%") == 0
