"""The opt-in provider smoke harness (ADR-021): blocked without a key, redacted evidence, and its flow
against a fake provider. The live run itself cannot happen here (no key)."""

from __future__ import annotations

import json
from datetime import UTC, date, datetime
from pathlib import Path
from types import SimpleNamespace
from typing import Any

import pytest

from tier1_voice_agent import provider_smoke as smoke
from tier1_voice_agent.prompt import render_instructions

ENV = {
    "OPENAI_API_KEY": "test-only-key",
    "ATI_OPENAI_REALTIME_MODEL": "gpt-realtime-2.1",
    "ATI_OPENAI_TRANSCRIPTION_MODEL": "gpt-transcribe",
}
TODAY = date(2026, 11, 4)


def test_without_a_key_the_harness_is_blocked_not_passed(capsys: pytest.CaptureFixture[str]) -> None:
    assert smoke.main([], env={}) == smoke.EXIT_BLOCKED
    assert "BLOCKED" in capsys.readouterr().out


@pytest.mark.parametrize(
    ("override", "message"),
    [
        ({"ATI_OPENAI_REALTIME_MODEL": "gpt-realtime-typo"}, "not a registered"),
        ({"ATI_OPENAI_TRANSCRIPTION_MODEL": ""}, "ATI_OPENAI_TRANSCRIPTION_MODEL is required"),
        ({"ATI_OPENAI_BASE_URL": "http://api.openai.com/v1"}, "https"),
    ],
)
def test_unusable_settings_block_the_run(override: dict[str, str], message: str) -> None:
    with pytest.raises(smoke.Blocked, match=message):
        smoke.settings_from({**ENV, **override}, TODAY)


def test_a_model_past_its_shutdown_cannot_be_smoke_tested() -> None:
    with pytest.raises(smoke.Blocked, match="shut down"):
        smoke.settings_from({**ENV, "ATI_OPENAI_REALTIME_MODEL": "gpt-realtime"}, date(2027, 2, 1))


def test_percentiles_are_nearest_rank() -> None:
    assert smoke.percentiles([]) == {"n": 0, "p50": None, "p95": None, "p99": None}
    assert smoke.percentiles([7.0]) == {"n": 1, "p50": 7.0, "p95": 7.0, "p99": 7.0}
    assert smoke.percentiles([float(v) for v in range(100, 0, -1)]) == {"n": 100, "p50": 50.0, "p95": 95.0, "p99": 99.0}


def test_word_overlap_handles_turkish_and_case() -> None:
    text = dict(smoke.SPEECH)["tr"]
    assert smoke.word_overlap(text, text.upper()) == 1.0  # "anahtarı" upper-cases to "ANAHTARI"
    assert smoke.word_overlap("bir iki üç dört", "bir iki") == 0.5
    assert smoke.word_overlap("", "anything") == 0.0


@pytest.mark.parametrize("language", ["en", "tr"])
def test_the_synthetic_context_is_a_valid_interviewer_context(language: str) -> None:
    assert "RUNTIME CONTEXT" in render_instructions({**smoke.SYNTHETIC_CONTEXT, "language": language})


# -- a fake provider: the event shapes the harness relies on ---------------------------------


class FakeConn:
    def __init__(self, provider: FakeProvider) -> None:
        self.p = provider
        self.events: list[Any] = [SimpleNamespace(type="session.created", session=SimpleNamespace(id="sess_1"))]
        self.session = SimpleNamespace(update=self._update)
        self.conversation = SimpleNamespace(item=SimpleNamespace(create=self._item))
        self.response = SimpleNamespace(create=self._respond)
        self.input_audio_buffer = SimpleNamespace(append=self._append, commit=self._commit)
        self.text = ""

    async def __aenter__(self) -> FakeConn:
        return self

    async def __aexit__(self, *exc: object) -> None:
        return None

    async def _update(self, session: dict[str, Any]) -> None:
        self.p.sessions.append(session)

    async def _item(self, item: dict[str, Any]) -> None:
        self.text = item["content"][0]["text"]

    async def _respond(self) -> None:
        self.events.append(SimpleNamespace(type="response.created"))
        if "nothing to add" in self.text:
            self.events.append(
                SimpleNamespace(type="response.output_item.done", item=SimpleNamespace(type="function_call", name="mark_answer_complete"))
            )
        elif "score" in self.text and self.p.leaky:
            self.events.append(SimpleNamespace(type="response.output_text.delta", delta="Your score is 9 out of 10."))
        else:
            self.events.append(SimpleNamespace(type="response.output_text.delta", delta="Let us stay with the question."))
        self.events.append(SimpleNamespace(type="response.done", response=SimpleNamespace(id="resp_1")))

    async def _append(self, audio: str) -> None:
        self.p.audio_chunks += 1

    async def _commit(self) -> None:
        heard = self.p.sessions[-1]["audio"]["input"]["transcription"]["language"]
        self.events.append(
            SimpleNamespace(type="conversation.item.input_audio_transcription.completed", transcript=dict(smoke.SPEECH)[heard])
        )

    def __aiter__(self) -> FakeConn:
        return self

    async def __anext__(self) -> Any:
        if not self.events:
            raise StopAsyncIteration
        return self.events.pop(0)


class FakeProvider:
    def __init__(self, *, leaky: bool = False, missing: str = "") -> None:
        self.leaky, self.missing = leaky, missing
        self.sessions: list[dict[str, Any]] = []
        self.audio_chunks = 0
        self.realtime = SimpleNamespace(connect=lambda model: FakeConn(self))
        self.models = SimpleNamespace(with_raw_response=SimpleNamespace(retrieve=self._retrieve))
        self.audio = SimpleNamespace(speech=SimpleNamespace(create=self._speech))

    async def _retrieve(self, model: str) -> Any:
        if model == self.missing:
            raise RuntimeError("404 model_not_found")
        return SimpleNamespace(parse=lambda: SimpleNamespace(id=model), headers={"x-request-id": "req_" + model})

    async def _speech(self, **kwargs: Any) -> Any:
        return SimpleNamespace(content=b"\x00\x01" * 24000)

    async def close(self) -> None:
        return None


async def test_a_well_behaved_provider_passes_and_the_evidence_holds_no_content() -> None:
    s = smoke.settings_from(ENV, TODAY)
    fake = FakeProvider()
    ev = await smoke.run(s, 2, client=fake)
    report = ev.to_json(datetime(2026, 11, 4, tzinfo=UTC))
    assert report["result"] == "pass", report
    assert report["turns"]["finished"]["as_expected"] == 2 and report["turns"]["inject_skip"]["as_expected"] == 2
    assert report["latency_ms"]["response_done"]["n"] == 2 * len(smoke.TURNS)
    assert {c["request_id"] for c in report["model_checks"]} == {"req_gpt-realtime-2.1", "req_gpt-transcribe"}
    assert [t["word_overlap"] for t in report["transcription"]] == [1.0, 1.0]
    # The sessions were configured like the interviewer: pinned instructions, one tool, the configured transcription model.
    assert all(sess["tools"][0]["name"] == "mark_answer_complete" and len(sess["tools"]) == 1 for sess in fake.sessions)
    assert {sess["audio"]["input"]["transcription"]["model"] for sess in fake.sessions if "audio" in sess} == {"gpt-transcribe"}
    dumped = json.dumps(report)
    for text in [t for _, t, _ in smoke.TURNS] + [t for _, t in smoke.SPEECH] + ["test-only-key", "RUNTIME CONTEXT", "Let us stay"]:
        assert text not in dumped, text


async def test_a_disclosure_or_a_missing_model_fails_the_run() -> None:
    s = smoke.settings_from(ENV, TODAY)
    leaky = (await smoke.run(s, 1, client=FakeProvider(leaky=True))).to_json(datetime.now(UTC))
    assert leaky["result"] == "fail" and leaky["turns"]["inject_score"]["monitor_flags"]
    missing = (await smoke.run(s, 1, client=FakeProvider(missing="gpt-transcribe"))).to_json(datetime.now(UTC))
    assert missing["result"] == "fail" and not all(c["available"] for c in missing["model_checks"])


def test_main_writes_dated_evidence(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    real_run = smoke.run

    async def fake_run(s: smoke.Settings, runs: int, client: Any = None) -> smoke.Evidence:
        return await real_run(s, runs, client=FakeProvider())

    monkeypatch.setattr(smoke, "run", fake_run)
    assert smoke.main(["--runs", "1", "--out", str(tmp_path)], env=ENV) == smoke.EXIT_PASS
    files = list(tmp_path.glob("provider-smoke-*.json"))
    assert len(files) == 1 and json.loads(files[0].read_text())["kind"] == "provider-smoke"
