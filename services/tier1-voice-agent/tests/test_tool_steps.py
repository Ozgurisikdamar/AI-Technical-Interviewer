"""The model gets one tool step per turn and cannot chain calls (single-tool policy, ADR-011).

livekit-agents counts steps from 1 and lets the reply to a tool result call tools again until
num_steps >= max_tool_steps + 1; max_tool_steps=1 therefore still allows one chained call. With
max_tool_steps=0 the reply to a tool result is generated with tool_choice="none".
"""

from __future__ import annotations

import uuid
from typing import Any

from livekit.agents import Agent, AgentSession, APIConnectOptions, function_tool, llm
from livekit.agents.types import DEFAULT_API_CONNECT_OPTIONS, NOT_GIVEN, NotGivenOr

from tier1_voice_agent.engines import MAX_TOOL_STEPS


class ChainingLLM(llm.LLM[Any]):
    """A model that calls the tool whenever it is allowed to, including in its reply to a tool result."""

    def __init__(self) -> None:
        super().__init__()
        self.tool_choices: list[str] = []

    @property
    def model(self) -> str:
        return "chaining-llm"

    @property
    def provider(self) -> str:
        return "test"

    def chat(
        self,
        *,
        chat_ctx: llm.ChatContext,
        tools: list[Any] | None = None,
        conn_options: APIConnectOptions = DEFAULT_API_CONNECT_OPTIONS,
        parallel_tool_calls: NotGivenOr[bool] = NOT_GIVEN,
        tool_choice: NotGivenOr[Any] = NOT_GIVEN,
        extra_kwargs: NotGivenOr[dict[str, Any]] = NOT_GIVEN,
    ) -> llm.LLMStream:
        choice = tool_choice if isinstance(tool_choice, str) else "auto"
        self.tool_choices.append(choice)
        return _ChainingStream(self, chat_ctx=chat_ctx, tools=tools or [], conn_options=conn_options, call=choice != "none")


class _ChainingStream(llm.LLMStream):
    def __init__(self, model: llm.LLM[Any], *, call: bool, **kwargs: Any) -> None:
        super().__init__(model, **kwargs)
        self._call = call

    async def _run(self) -> None:
        rid = f"chain-{uuid.uuid4().hex[:8]}"
        if self._call:
            call = llm.FunctionToolCall(name="mark_answer_complete", arguments="{}", call_id=f"call_{uuid.uuid4().hex[:8]}")
            self._event_ch.send_nowait(llm.ChatChunk(id=rid, delta=llm.ChoiceDelta(role="assistant", tool_calls=[call])))
        else:
            self._event_ch.send_nowait(llm.ChatChunk(id=rid, delta=llm.ChoiceDelta(role="assistant", content="Thank you.")))


class CountingAgent(Agent):
    def __init__(self) -> None:
        super().__init__(instructions="test interviewer")
        self.calls = 0

    @function_tool(name="mark_answer_complete", description="Record that the candidate finished answering.")
    async def mark_answer_complete(self) -> str:
        self.calls += 1
        return "The answer has been recorded."


async def test_the_model_cannot_chain_tool_calls() -> None:
    model, agent = ChainingLLM(), CountingAgent()
    async with AgentSession[None](llm=model, max_tool_steps=MAX_TOOL_STEPS) as session:
        await session.start(agent)
        await session.run(user_input="That is all I wanted to say.")
    assert agent.calls == 1, f"the tool ran {agent.calls} times in one turn"
    assert model.tool_choices[0] == "auto"
    assert model.tool_choices[1:] and set(model.tool_choices[1:]) == {"none"}, model.tool_choices
