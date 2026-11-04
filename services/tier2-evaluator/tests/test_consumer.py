"""Redis Streams consumer against a real local redis-server (ADR-006, ADR-022 decision 2)."""

from __future__ import annotations

import asyncio
from collections.abc import Callable

import pytest
import redis.asyncio as aioredis

from tier2_evaluator.consumer import DLQ, GROUP, STREAM, Consumer, RetryLater, Undeliverable

pytestmark = pytest.mark.redis


class Recorder:
    def __init__(self, behaviour: Callable[[int], None] | None = None, *, delay: float = 0.0) -> None:
        self.calls: list[dict[str, str]] = []
        self.dead: list[tuple[dict[str, str], str]] = []
        self.dead_failures = 0
        self._behaviour, self._delay = behaviour, delay

    async def handle(self, fields: dict[str, str]) -> None:
        self.calls.append(fields)
        if self._delay:
            await asyncio.sleep(self._delay)
        if self._behaviour is not None:
            self._behaviour(len(self.calls))

    async def dead_letter(self, fields: dict[str, str], reason: str) -> None:
        if self.dead_failures:
            self.dead_failures -= 1
            raise ConnectionError("control plane down")
        self.dead.append((fields, reason))


def consumer(client: aioredis.Redis, handler: Recorder, name: str = "c1", **kw: int) -> Consumer:
    opts = {"max_deliveries": 3, "visibility_timeout_ms": 150, "block_ms": 50, **kw}
    return Consumer(client, handler, consumer_name=name, **opts)


async def publish(client: aioredis.Redis, n: int = 1) -> list[str]:
    ids = []
    for i in range(n):
        raw = await client.xadd(
            STREAM, {"event_id": f"evt_{i}", "event_type": "interview.completed", "session_id": f"ses_{i}", "payload": "{}"}
        )
        ids.append(raw.decode())
    return ids


async def pending(client: aioredis.Redis) -> int:
    info = await client.xpending(STREAM, GROUP)
    return int(info["pending"])


async def test_entries_published_before_the_group_exists_are_handled_and_acknowledged(redis_client: aioredis.Redis) -> None:
    await publish(redis_client, 2)
    h = Recorder()
    c = consumer(redis_client, h)
    await c.ensure_group()
    await c.ensure_group()  # idempotent
    assert await c.run_once() == 2
    assert [f["event_id"] for f in h.calls] == ["evt_0", "evt_1"]
    assert await pending(redis_client) == 0 and await redis_client.xlen(DLQ) == 0


async def test_retry_later_leaves_the_entry_pending_until_reclaimed(redis_client: aioredis.Redis) -> None:
    def first_fails(n: int) -> None:
        if n == 1:
            raise RetryLater("control_plane_503")

    h = Recorder(first_fails)
    c = consumer(redis_client, h)
    await c.ensure_group()
    await publish(redis_client)
    await c.run_once()
    assert await pending(redis_client) == 1
    assert await c.reclaim() == []  # not idle long enough yet
    await asyncio.sleep(0.2)
    await c.run_once()  # reclaimed (second delivery) and handled
    assert len(h.calls) == 2 and await pending(redis_client) == 0


async def test_crashed_consumer_entries_are_reclaimed_by_another(redis_client: aioredis.Redis) -> None:
    c1, c2 = consumer(redis_client, Recorder(), "c1"), consumer(redis_client, Recorder(), "c2")
    await c1.ensure_group()
    await publish(redis_client)
    # c1 reads the entry and dies before acknowledging it.
    await redis_client.xreadgroup(GROUP, "c1", {STREAM: ">"}, count=1)
    await asyncio.sleep(0.2)
    reclaimed = await c2.reclaim()
    assert [d.deliveries for d in reclaimed] == [2]


async def test_poison_entry_is_dead_lettered_after_max_deliveries(redis_client: aioredis.Redis) -> None:
    def always(n: int) -> None:
        raise RuntimeError("bug")

    h = Recorder(always)
    c = consumer(redis_client, h, max_deliveries=3)
    await c.ensure_group()
    (entry,) = await publish(redis_client)
    await c.run_once()
    for _ in range(3):
        await asyncio.sleep(0.2)
        await c.run_once()
    assert len(h.calls) == 3  # handled at most max_deliveries times
    assert [r for _, r in h.dead] == ["max_deliveries"]
    assert await pending(redis_client) == 0
    ((_, fields),) = await redis_client.xrange(DLQ)
    assert fields[b"source_entry_id"].decode() == entry and fields[b"dead_letter_reason"] == b"max_deliveries"
    assert fields[b"deliveries"] == b"4" and fields[b"session_id"] == b"ses_0"


async def test_undeliverable_entry_is_dead_lettered_at_once(redis_client: aioredis.Redis) -> None:
    def malformed(n: int) -> None:
        raise Undeliverable("malformed_event")

    h = Recorder(malformed)
    c = consumer(redis_client, h)
    await c.ensure_group()
    await publish(redis_client)
    await c.run_once()
    assert len(h.calls) == 1 and [r for _, r in h.dead] == ["malformed_event"]
    assert await pending(redis_client) == 0 and await redis_client.xlen(DLQ) == 1


async def test_failed_dead_letter_report_keeps_the_entry_pending(redis_client: aioredis.Redis) -> None:
    def malformed(n: int) -> None:
        raise Undeliverable("malformed_event")

    h = Recorder(malformed)
    h.dead_failures = 1
    c = consumer(redis_client, h)
    await c.ensure_group()
    await publish(redis_client)
    await c.run_once()
    assert h.dead == [] and await pending(redis_client) == 1 and await redis_client.xlen(DLQ) == 0
    await asyncio.sleep(0.2)
    await c.run_once()
    assert [r for _, r in h.dead] == ["malformed_event"] and await pending(redis_client) == 0 and await redis_client.xlen(DLQ) == 1


async def test_slow_handling_keeps_its_claim(redis_client: aioredis.Redis) -> None:
    slow = Recorder(delay=0.6)
    c1, c2 = consumer(redis_client, slow, "c1"), consumer(redis_client, Recorder(), "c2")
    await c1.ensure_group()
    await publish(redis_client)
    task = asyncio.create_task(c1.run_once())
    stolen = []
    for _ in range(8):
        await asyncio.sleep(0.08)
        stolen += await c2.reclaim()
    await task
    assert stolen == [] and len(slow.calls) == 1 and await pending(redis_client) == 0


async def test_other_event_types_are_acknowledged_like_any_entry(redis_client: aioredis.Redis) -> None:
    h = Recorder()
    c = consumer(redis_client, h)
    await c.ensure_group()
    await redis_client.xadd(STREAM, {"event_id": "evt_a", "event_type": "answer.finalized", "session_id": "ses_a", "payload": "{}"})
    await c.run_once()
    assert await pending(redis_client) == 0


async def test_run_stops_when_asked_and_recreates_a_missing_group(redis_client: aioredis.Redis) -> None:
    h = Recorder()
    c = consumer(redis_client, h)
    stop = asyncio.Event()
    task = asyncio.create_task(c.run(stop))
    await asyncio.sleep(0.1)
    await redis_client.xgroup_destroy(STREAM, GROUP)
    await publish(redis_client)
    for _ in range(40):
        if h.calls:
            break
        await asyncio.sleep(0.05)
    stop.set()
    await asyncio.wait_for(task, timeout=2)
    assert len(h.calls) == 1
