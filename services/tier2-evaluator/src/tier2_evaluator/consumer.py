"""Redis Streams consumer (ADR-006, ADR-022 decision 2).

Entries of ati:tier2:events are read with the consumer group "tier2". An entry is acknowledged
once it has been handled. An entry that fails stays pending; once it has been idle longer than the
visibility timeout, any consumer reclaims it (XAUTOCLAIM). While an entry is being handled, its
idle time is reset regularly, so that a slow evaluation is not reclaimed by another consumer. An
entry delivered more than max_deliveries times, or one that can never succeed, is moved to
ati:tier2:dlq and reported through the handler's dead-letter path.

Entries carry identifiers only (ADR-015); nothing here logs their payload.
"""

from __future__ import annotations

import asyncio
import contextlib
import logging
from dataclasses import dataclass
from typing import Protocol

import redis.asyncio as aioredis
from redis.exceptions import ConnectionError as RedisConnectionError
from redis.exceptions import ResponseError
from redis.exceptions import TimeoutError as RedisTimeoutError

STREAM = "ati:tier2:events"
GROUP = "tier2"
DLQ = "ati:tier2:dlq"
DLQ_MAXLEN = 10_000

log = logging.getLogger("tier2.consumer")


class RetryLater(Exception):
    """Handling failed for a reason that may pass; the entry stays pending and is retried."""


class Undeliverable(Exception):
    """Handling can never succeed (for example a malformed entry); the entry is dead-lettered now."""


class Handler(Protocol):
    async def handle(self, fields: dict[str, str]) -> None: ...

    async def dead_letter(self, fields: dict[str, str], reason: str) -> None: ...


@dataclass(frozen=True)
class Delivery:
    id: str
    fields: dict[str, str]
    deliveries: int


class Consumer:
    def __init__(
        self,
        client: aioredis.Redis,
        handler: Handler,
        *,
        consumer_name: str,
        max_deliveries: int = 5,
        visibility_timeout_ms: int = 120_000,
        block_ms: int = 5_000,
        batch: int = 10,
    ) -> None:
        self._redis, self._handler, self._name = client, handler, consumer_name
        self._max, self._visibility, self._block, self._batch = max_deliveries, visibility_timeout_ms, block_ms, batch

    async def ensure_group(self) -> None:
        """Create the group at the start of the stream: entries published before the first worker
        started are still evaluated."""
        try:
            await self._redis.xgroup_create(STREAM, GROUP, id="0", mkstream=True)
        except ResponseError as e:
            if "BUSYGROUP" not in str(e):
                raise

    async def run(self, stop: asyncio.Event) -> None:
        backoff = 0.5
        await self._until_ready(stop)
        while not stop.is_set():
            try:
                await self.run_once()
                backoff = 0.5
            except ResponseError as e:
                if "NOGROUP" not in str(e):
                    raise
                log.warning("consumer group missing; recreating it")
                await self.ensure_group()
            except (RedisConnectionError, RedisTimeoutError, OSError):
                log.warning("redis unavailable; retrying", extra={"retry_in_s": backoff})
                with contextlib.suppress(TimeoutError):
                    await asyncio.wait_for(stop.wait(), timeout=backoff)
                backoff = min(backoff * 2, 15.0)

    async def _until_ready(self, stop: asyncio.Event) -> None:
        backoff = 0.5
        while not stop.is_set():
            try:
                await self.ensure_group()
                return
            except (RedisConnectionError, RedisTimeoutError, OSError):
                log.warning("redis unavailable; retrying", extra={"retry_in_s": backoff})
                with contextlib.suppress(TimeoutError):
                    await asyncio.wait_for(stop.wait(), timeout=backoff)
                backoff = min(backoff * 2, 15.0)

    async def run_once(self) -> int:
        """Handle reclaimed entries first, then wait for new ones. Returns how many were handled."""
        handled = 0
        for d in await self.reclaim():
            await self._process(d)
            handled += 1
        resp = await self._redis.xreadgroup(GROUP, self._name, {STREAM: ">"}, count=self._batch, block=self._block)
        for _stream, entries in resp or []:
            for entry_id, fields in entries:
                await self._process(Delivery(_str(entry_id), _fields(fields), 1))
                handled += 1
        return handled

    async def reclaim(self) -> list[Delivery]:
        """Entries left pending by any consumer for longer than the visibility timeout."""
        result = await self._redis.xautoclaim(STREAM, GROUP, self._name, min_idle_time=self._visibility, start_id="0-0", count=self._batch)
        entries, deleted = result[1], (result[2] if len(result) > 2 else [])
        out: list[Delivery] = []
        for entry_id, fields in entries:
            if not fields:  # trimmed from the stream while pending: nothing left to handle
                await self._redis.xack(STREAM, GROUP, entry_id)
                continue
            pending = await self._redis.xpending_range(STREAM, GROUP, min=entry_id, max=entry_id, count=1)
            times = int(pending[0]["times_delivered"]) if pending else 1
            out.append(Delivery(_str(entry_id), _fields(fields), times))
        if deleted:
            await self._redis.xack(STREAM, GROUP, *deleted)
        return out

    async def _process(self, d: Delivery) -> None:
        if d.deliveries > self._max:
            await self._dead_letter(d, "max_deliveries")
            return
        heartbeat = asyncio.create_task(self._keep_claimed(d.id))
        try:
            await self._handler.handle(d.fields)
        except Undeliverable as e:
            await self._dead_letter(d, str(e) or "undeliverable")
            return
        except RetryLater as e:
            log.warning("entry left pending for retry", extra={"entry_id": d.id, "deliveries": d.deliveries, "reason": str(e)})
            return
        except Exception:
            # An unexpected failure is retried like any other; after max_deliveries it is dead-lettered.
            log.exception("entry handling failed", extra={"entry_id": d.id, "deliveries": d.deliveries})
            return
        finally:
            heartbeat.cancel()
            with contextlib.suppress(asyncio.CancelledError):
                await heartbeat
        await self._redis.xack(STREAM, GROUP, d.id)

    async def _keep_claimed(self, entry_id: str) -> None:
        """Reset the entry's idle time while it is being handled (XCLAIM JUSTID does not count as a
        delivery)."""
        interval = max(self._visibility / 3000, 0.05)
        while True:
            await asyncio.sleep(interval)
            with contextlib.suppress(RedisConnectionError, RedisTimeoutError, OSError):
                await self._redis.xclaim(STREAM, GROUP, self._name, min_idle_time=0, message_ids=[entry_id], idle=0, justid=True)

    async def _dead_letter(self, d: Delivery, reason: str) -> None:
        """Report first, then move: if reporting fails, the entry stays pending and is retried, so
        the interview's fallback to human review is never lost."""
        try:
            await self._handler.dead_letter(d.fields, reason)
        except Exception:
            log.exception("dead-letter report failed; entry left pending", extra={"entry_id": d.id})
            return
        fields: dict[str, str] = {**d.fields, "dead_letter_reason": reason, "source_entry_id": d.id, "deliveries": str(d.deliveries)}
        await self._redis.xadd(DLQ, fields, maxlen=DLQ_MAXLEN, approximate=True)  # type: ignore[arg-type]
        await self._redis.xack(STREAM, GROUP, d.id)
        log.warning("entry dead-lettered", extra={"entry_id": d.id, "deliveries": d.deliveries, "reason": reason})


def _str(value: bytes | str) -> str:
    return value.decode() if isinstance(value, bytes) else value


def _fields(raw: dict[bytes | str, bytes | str]) -> dict[str, str]:
    return {_str(k): _str(v) for k, v in raw.items()}
