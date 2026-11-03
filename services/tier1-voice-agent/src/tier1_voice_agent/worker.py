"""LiveKit worker: explicit dispatch only (ADR-011, ADR-016)."""

from __future__ import annotations

import json
import logging

from livekit.agents import AgentServer, JobContext

from .config import Config
from .control_plane import ControlPlane
from .controller import InterviewController
from .engines import VoiceEngine
from .engines.openai_realtime import OpenAIRealtimeEngine
from .engines.scripted import ScriptedEngine

log = logging.getLogger("tier1.worker")


class MetadataError(Exception):
    pass


def parse_metadata(raw: str) -> tuple[str, str]:
    """Dispatch metadata must contain exactly session_id and claim_nonce (no grants or secrets)."""
    try:
        meta = json.loads(raw or "{}")
    except json.JSONDecodeError as e:
        raise MetadataError("dispatch metadata is not JSON") from e
    if not isinstance(meta, dict) or set(meta) != {"session_id", "claim_nonce"}:
        raise MetadataError("dispatch metadata must contain exactly session_id and claim_nonce")
    sid, nonce = meta["session_id"], meta["claim_nonce"]
    if not (isinstance(sid, str) and sid.startswith("ses_") and isinstance(nonce, str) and len(nonce) >= 32):
        raise MetadataError("dispatch metadata values are malformed")
    return sid, nonce


def make_engine(cfg: Config) -> VoiceEngine:
    if cfg.engine == "scripted":
        if cfg.production:
            raise RuntimeError("scripted engine refused in production")
        return ScriptedEngine()
    assert cfg.openai_api_key is not None
    return OpenAIRealtimeEngine(
        api_key=cfg.openai_api_key, model=cfg.openai_model, voice=cfg.openai_voice, transcription_model=cfg.openai_transcription_model
    )


def build_server(cfg: Config) -> AgentServer:
    server = AgentServer(
        ws_url=cfg.livekit_url,
        api_key=cfg.livekit_api_key,
        api_secret=cfg.livekit_api_secret,
        port=cfg.health_port,
        http_proxy=cfg.http_proxy,
    )  # explicit: never inherit HTTPS_PROXY

    @server.rtc_session(agent_name=cfg.agent_name)
    async def entry(ctx: JobContext) -> None:
        try:
            session_id, nonce = parse_metadata(ctx.job.metadata)
        except MetadataError as e:
            log.warning("refusing job", extra={"reason": str(e)})
            return
        await ctx.connect()
        controller = InterviewController(
            room=ctx.room,
            job_id=ctx.job.id,
            cp=ControlPlane(cfg.control_plane_url, cfg.service_token, proxy=cfg.http_proxy),
            engine=make_engine(cfg),
            session_id=session_id,
            nonce=nonce,
        )

        async def on_shutdown() -> None:
            controller.stop()

        ctx.add_shutdown_callback(on_shutdown)
        await controller.run()
        ctx.shutdown("interview controller finished")

    return server
