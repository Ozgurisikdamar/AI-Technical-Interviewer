# ADR-021 — Provider model registry, endpoint pinning and the full-duplex assessment

**Status:** Proposed — pending architecture review
**Date:** 2026-11-04
**Relates to:** ADR-004, ADR-011, ADR-020

## Context

- Phase 1 defaulted the Tier-1 agent to `gpt-realtime` (speech-to-speech) and `gpt-4o-transcribe` (input-audio transcription). OpenAI has announced shutdowns for both. Source: [OpenAI deprecations](https://developers.openai.com/api/docs/deprecations), checked 2026-11-04.

  | Model | Announced | Shutdown | Replacement |
  |---|---|---|---|
  | `gpt-realtime` | 2026-07-20 | 2027-01-20 | `gpt-realtime-2.1` |
  | `gpt-4o-transcribe` | 2026-08-26 | 2027-02-26 | `gpt-transcribe` or `gpt-live-transcribe` |

- No model has been exercised against the live provider in this repository, because no provider credentials are available. Whatever we choose is a documented candidate, not a verified model.
- If the agent passes no `base_url`, the realtime client falls back to the `OPENAI_BASE_URL` environment variable. That fallback sits in livekit-agents (`livekit/agents/llm/_realtime/openai.py`). So one inherited environment variable could send the provider key and the candidate's audio to another host.
- The handover asks whether the current model architecture satisfies a full-duplex requirement.

## Decision

1. **Model registry**
   - `contracts/models/model-registry.v1.json` lists every provider model a service may be configured with. Its schema is `contracts/schemas/models/v1/model-registry.schema.json`.
   - Each entry records the provider, the purpose (`tier1_realtime`, `tier1_transcription` or `tier2_evaluator`), the status, the shutdown date, the replacements, the evidence and a source link.
   - Statuses:
     - `candidate`: documented by the provider, but unverified here.
     - `verified`: requires an evidence file under `docs/status/evidence/`, produced by the opt-in provider smoke harness. The file holds the model ID, request IDs and latency percentiles; never audio or transcripts.
     - `deprecated`: requires a shutdown date and at least one replacement.
   - The agent ships a synced copy (`tools/dev/sync-agent-contracts.sh`), and a drift test keeps the two equal.
2. **No default model identifiers**
   - The `openai` engine requires `ATI_OPENAI_REALTIME_MODEL` and `ATI_OPENAI_TRANSCRIPTION_MODEL`. The agent checks both against the registry for their purpose:

     | Case | dev / test | staging | production |
     |---|---|---|---|
     | Model not in the registry | warning | refused | refused |
     | Model past its shutdown date | refused | refused | refused |
     | Model deprecated, before shutdown | warning | warning | refused (not `verified`) |
     | Model not `verified` | allowed | allowed | refused |

   - The deprecation warning gives the shutdown date and the replacements, and says "soon" within 90 days of shutdown. All warnings are logged at startup.
   - Like the refusal that waits for a legally approved notice (ADR-020), production cannot start with the `openai` engine until a model is verified.
3. **Pinned provider endpoint**
   - `ATI_OPENAI_BASE_URL` (default `https://api.openai.com/v1`) is passed to the client explicitly.
   - It must use https; http is accepted only for a local endpoint in dev and test.
   - An `OPENAI_BASE_URL` that differs from it is refused as ambiguous.
   - Production uses the default endpoint. Any other endpoint (a data-residency endpoint, a gateway) is a new decision with its own processor review.
4. **One tool call per turn**
   - livekit-agents counts steps from 1. The reply to a tool result may call tools again until `num_steps >= max_tool_steps + 1`. So `max_tool_steps = 1` still allows one chained call, which was observed in `tests/test_tool_steps.py`.
   - The agent therefore uses `max_tool_steps = 0`: the reply to a tool result is generated with `tool_choice="none"`.
   - The framework warns after every tool call that the step limit was reached. That is the policy working, so the agent's log filter drops the warning.
5. **Full duplex: not provided, and not claimed**
   - The Tier-1 path is turn-based with barge-in:
     - the provider's semantic VAD ends the candidate's turn;
     - the interviewer speaks approved prompts;
     - the candidate can interrupt the interviewer;
     - the interviewer neither speaks over the candidate nor backchannels.
   - That is half duplex with interruption, so documents and UI must not call it full duplex.
   - The installed OpenAI plugin also ships a duplex model client (GPT-Live, `gpt_live_model.py`). Adopting it would conflict with these Phase 1 invariants:
     - approved questions are spoken verbatim, injected one at a time by the controller;
     - instructions and turn profiles change per question;
     - the single-tool, no-chaining completion protocol, decided by the server;
     - pause, resume and interruption semantics bound to state-machine transitions;
     - candidate text that never travels over the media path (ADR-018).
   - Full duplex is therefore a product requirement change, not a configuration change. It would be prototyped only as a separate engine, and only when all of these exist:
     - a written requirement;
     - provider credentials;
     - a benchmark against the current path. It must cover: end-of-turn and barge-in-stop latency (p50, p95, p99); the false-interruption rate; transcript accuracy in Turkish and English; and adherence to verbatim prompts.

## Consequences

- Model rotation becomes a reviewed registry change backed by evidence. Deprecations surface at every start instead of on the shutdown day.
- Production stays refused for the `openai` engine until the smoke harness has produced evidence for a model and someone has reviewed it.
- The Tier-2 evaluator's models (Milestone B) are added to the same registry under `tier2_evaluator`.
- Two conclusions rest on documentation and the installed SDK, not on calls to the provider:
  - which transcription replacement works as the input-audio transcription model of a conversation session;
  - how much latency the reasoning model `gpt-realtime-2.1` adds.

  The smoke harness has to settle both.

## Verification

- Contract tests check the schema, the valid and invalid fixtures, and the shipped registry: unique entries, replacements that resolve within the same purpose, and evidence present for every verified model.
- Agent config tests cover each rule in decision 2 and the endpoint pin.
- An engine test checks that the client uses the configured endpoint even when `OPENAI_BASE_URL` is set.
- `tests/test_tool_steps.py` shows a model that tries to chain calls gets exactly one tool execution per turn.
