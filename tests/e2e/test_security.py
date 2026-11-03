"""Server-enforced media permissions, intruders, deleted rooms and forged webhooks, against the real
LiveKit OSS server and control plane (ADR-016, ADR-018, THREAT_MODEL_ADDENDUM_PHASE1)."""

from __future__ import annotations

import asyncio
import base64
import datetime
import hashlib
import json
import os
import time
import uuid
from collections.abc import Coroutine
from typing import Any

import httpx
import pytest
from livekit import api, rtc

from candidate import Candidate, calibrate, listen_to_prompt
from conftest import Db

pytestmark = pytest.mark.asyncio(loop_scope="session")

PUBLISH_WAIT_S = 12.0  # the server never answers a refused publication; the SDK gives up after 10 s


def livekit_api() -> api.LiveKitAPI:
    return api.LiveKitAPI(
        os.environ["ATI_E2E_LIVEKIT_API_URL"], os.environ["ATI_E2E_LIVEKIT_API_KEY"], os.environ["ATI_E2E_LIVEKIT_API_SECRET"]
    )


async def participant(room: str, identity: str) -> api.ParticipantInfo | None:
    lk = livekit_api()
    try:
        res = await lk.room.list_participants(api.ListParticipantsRequest(room=room))
    finally:
        await lk.aclose()
    return next((p for p in res.participants if p.identity == identity), None)


async def refused(publication: Coroutine[Any, Any, Any]) -> bool:
    """True when the server never confirms the publication (the SDK raises, or times out)."""
    try:
        await asyncio.wait_for(publication, PUBLISH_WAIT_S)
    except Exception:  # PublishTrackError ("no response received from the server") or a timeout
        return True
    return False


async def test_the_server_enforces_microphone_only_publishing(candidate: Candidate, db: Db) -> None:
    c = candidate
    room_name = db.one("SELECT room_name FROM ati.interview_sessions WHERE id = %s", c.interview_id)
    assert c.room is not None
    me = await participant(room_name, "candidate")
    assert me is not None
    perm = me.permission
    assert perm.can_publish and list(perm.can_publish_sources) == [api.TrackSource.MICROPHONE]
    assert not perm.can_publish_data and not perm.can_update_metadata and perm.can_subscribe
    # Camera, screen share and a second audio source are refused by the server, not by the client.
    source = rtc.VideoSource(64, 64)
    attempts = [
        c.room.local_participant.publish_track(
            rtc.LocalVideoTrack.create_video_track("camera", source), rtc.TrackPublishOptions(source=rtc.TrackSource.SOURCE_CAMERA)
        ),
        c.room.local_participant.publish_track(
            rtc.LocalVideoTrack.create_video_track("screen", source), rtc.TrackPublishOptions(source=rtc.TrackSource.SOURCE_SCREENSHARE)
        ),
        c.room.local_participant.publish_track(
            rtc.LocalAudioTrack.create_audio_track("screen-audio", rtc.AudioSource(48000, 1)),
            rtc.TrackPublishOptions(source=rtc.TrackSource.SOURCE_SCREENSHARE_AUDIO),
        ),
    ]
    assert all(await asyncio.gather(*(refused(a) for a in attempts)))
    # Data and metadata are dropped by the server (the client SDK does not report it).
    await c.room.local_participant.publish_data(b"synthetic data packet", reliable=True)
    await c.room.local_participant.set_metadata("synthetic metadata")
    await asyncio.sleep(1.0)
    me = await participant(room_name, "candidate")
    assert me is not None and me.metadata == ""
    assert [(t.source, t.type) for t in me.tracks] == [(api.TrackSource.MICROPHONE, api.TrackType.AUDIO)]


async def test_a_text_mode_candidate_cannot_publish_audio(stack: dict[str, str], staff_token: str, db: Db) -> None:
    c = Candidate()
    try:
        await c.create_and_join_api(staff_token, voice=False)
        rt = await c.realtime_session()
        assert rt["can_publish_audio"] is False
        room = rtc.Room()
        await room.connect(rt["livekit_url"], rt["token"])
        try:
            mic = rtc.LocalAudioTrack.create_audio_track("mic", rtc.AudioSource(48000, 1))
            assert await refused(
                room.local_participant.publish_track(mic, rtc.TrackPublishOptions(source=rtc.TrackSource.SOURCE_MICROPHONE))
            )
            room_name = db.one("SELECT room_name FROM ati.interview_sessions WHERE id = %s", c.interview_id)
            me = await participant(room_name, "candidate")
            assert me is not None and not me.permission.can_publish and list(me.tracks) == []
        finally:
            await room.disconnect()
    finally:
        await c.close()


async def _connect_minted(room_name: str, identity: str, *, agent: bool) -> tuple[rtc.Room, asyncio.Event]:
    token = (
        api.AccessToken(os.environ["ATI_E2E_LIVEKIT_API_KEY"], os.environ["ATI_E2E_LIVEKIT_API_SECRET"])
        .with_identity(identity)
        .with_kind("agent" if agent else "standard")
        .with_grants(api.VideoGrants(room_join=True, room=room_name, agent=agent))
        .to_jwt()
    )
    room = rtc.Room()
    gone = asyncio.Event()
    room.on("disconnected", lambda *_: gone.set())
    await room.connect("ws://localhost:" + os.environ["ATI_E2E_LIVEKIT_API_URL"].rsplit(":", 1)[1], token)
    return room, gone


async def test_participants_not_minted_by_the_control_plane_are_removed(candidate: Candidate, db: Db) -> None:
    c = candidate
    await calibrate(c)
    await listen_to_prompt(c)  # the dispatched agent now holds the lease
    room_name = db.one("SELECT room_name FROM ati.interview_sessions WHERE id = %s", c.interview_id)
    for identity, agent in (("intruder-standard", False), ("agent-intruder", True)):
        room, gone = await _connect_minted(room_name, identity, agent=agent)
        try:
            await asyncio.wait_for(gone.wait(), 10)
        finally:
            await room.disconnect()
        assert await participant(room_name, identity) is None
    audits = db.one(
        "SELECT count(*) FROM ati.audit_logs WHERE subject_id = %s AND action = 'media.unexpected_participant' AND outcome = 'denied'",
        c.interview_id,
    )
    assert audits == 2
    view = await c.view()
    assert view["state"] == "QUESTION_ACTIVE"  # the interview itself is unaffected


async def test_a_withdrawn_interview_room_cannot_be_rejoined(stack: dict[str, str], staff_token: str, db: Db) -> None:
    c = Candidate()
    try:
        await c.create_and_join_api(staff_token)
        await c.join()
        token = c.media_token
        v = await c.view()
        await c._call("POST", c._path("withdraw"), {"state_version": v["state_version"], "confirm": True})
        await asyncio.wait_for(c.disconnected.wait(), 10)  # the room is deleted, which ends the connection
        await c.leave()
        room = rtc.Room()
        with pytest.raises(Exception):  # noqa: B017 - the SDK reports a refused join as a generic connection error
            await asyncio.wait_for(room.connect(os.environ["ATI_E2E_LIVEKIT_API_URL"].replace("http", "ws"), token), 10)
        with pytest.raises(Exception):  # noqa: B017
            await c.realtime_session()  # and no new media token is issued
        assert db.one("SELECT state FROM ati.interview_sessions WHERE id = %s", c.interview_id) == "CANCELLED"
    finally:
        await c.close()


def _signed(body: bytes, key: str, secret: str) -> str:
    digest = base64.b64encode(hashlib.sha256(body).digest()).decode()
    return api.AccessToken(key, secret).with_ttl(datetime.timedelta(minutes=1)).with_sha256(digest).to_jwt()


async def test_forged_webhooks_are_refused(stack: dict[str, str], db: Db) -> None:
    url = os.environ["ATI_E2E_INTERNAL_URL"] + "/internal/v1/livekit/webhook"
    room = db.rows("SELECT room_name FROM ati.interview_sessions ORDER BY created_at DESC LIMIT 1")[0][0]
    body = json.dumps(
        {
            "id": f"EV_e2e_{uuid.uuid4().hex[:12]}",
            "event": "participant_left",
            "createdAt": str(int(time.time())),
            "room": {"name": room},
            "participant": {"identity": "candidate"},
        }
    ).encode()
    headers = {"Content-Type": "application/webhook+json"}
    async with httpx.AsyncClient(trust_env=False) as http:
        unsigned = await http.post(url, content=body, headers=headers)
        api_key = await http.post(
            url,
            content=body,
            headers={
                **headers,
                "Authorization": _signed(body, os.environ["ATI_E2E_LIVEKIT_API_KEY"], os.environ["ATI_E2E_LIVEKIT_API_SECRET"]),
            },
        )
    assert (unsigned.status_code, api_key.status_code) == (401, 401)
    assert db.one("SELECT count(*) FROM ati.webhook_events WHERE event_id = %s", json.loads(body)["id"]) == 0
