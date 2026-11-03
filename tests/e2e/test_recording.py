"""Recording fails closed against the real LiveKit server when no egress service exists (ADR-017).

The control plane is restarted with recording enabled and every deployment precondition declared,
the tenant opts in through ati-admin and the candidate consents. LiveKit OSS without an egress
service cannot record: the recording must fail visibly (audited, no audio anywhere) and the
interview must carry on. The control plane is restored afterwards.
"""

from __future__ import annotations

import asyncio
import subprocess
import time
from collections.abc import Iterator
from pathlib import Path

import pytest

from candidate import DONE, Candidate, calibrate, listen_to_prompt
from conftest import Db, release, stack_cmd

pytestmark = pytest.mark.asyncio(loop_scope="session")


def admin(state: Path, *args: str) -> None:
    owner = (Path(state).parents[0] / "pg" / "admin_url").read_text().strip().rsplit("/", 1)[0] + "/ati?sslmode=disable"
    subprocess.run([str(state / "bin" / "ati-admin"), *args, "--url", owner], check=True, capture_output=True, timeout=60)


@pytest.fixture
def recording_enabled(stack: dict[str, str]) -> Iterator[str]:
    state = Path(stack["ATI_E2E_STATE_DIR"])
    tenant = (state / "tenant_id").read_text().strip()
    admin(state, "tenant-recording", "--tenant", tenant, "--allowed=true")
    stack_cmd("cp-restart", extra_env={"ATI_DEV_RECORDING": "1"})
    try:
        yield tenant
    finally:
        admin(state, "tenant-recording", "--tenant", tenant, "--allowed=false")
        stack_cmd("cp-restart")


async def test_recording_fails_closed_without_an_egress_service(recording_enabled: str, staff_token: str, db: Db) -> None:
    c = Candidate()
    try:
        await c.create_and_join_api(staff_token, recording=True)
        await c.join()
        view = await calibrate(c)  # T05 asks the recorder to start
        asked = time.monotonic()  # the first question plays while the recording attempt runs
        # LiveKit never answers the start request; the recorder's egress timeout (10 s) fails it.
        deadline = time.monotonic() + 20
        rows: list[tuple[object, ...]] = []
        while time.monotonic() < deadline:
            rows = db.rows("SELECT status, egress_id, failure FROM ati.audio_recordings WHERE session_id = %s", c.interview_id)
            if rows and rows[0][0] != "starting":
                break
            await asyncio.sleep(0.5)
        assert len(rows) == 1 and rows[0][0] == "failed" and rows[0][1] is None and "timed out" in str(rows[0][2]), rows
        assert db.one("SELECT count(*) FROM ati.audit_logs WHERE subject_id = %s AND action = 'recording.failed'", c.interview_id) >= 1
        assert db.one("SELECT count(*) FROM ati.audit_logs WHERE subject_id = %s AND action = 'recording.start'", c.interview_id) == 1
        assert (await c.view())["recording_active"] is False
        await listen_to_prompt(c, since=asked)
        await c.say(DONE)  # the interview continues without a recording
        await c.wait_state("QUESTION_ACTIVE", timeout=25, activation_not=view["active_prompt"]["activation_id"])
        refs = db.one(
            "SELECT count(*) FROM ati.transcript_segments WHERE session_id = %s AND audio_recording_id IS NOT NULL", c.interview_id
        )
        assert refs == 0  # no segment points at audio that does not exist
        consent = db.one("SELECT granted FROM ati.consent_records WHERE session_id = %s AND purpose_id = 'audio_recording'", c.interview_id)
        assert consent is True
    finally:
        await release(c)
