# Technology Selection and Benchmark Plan

**Version:** 1.0  
**Status:** Initial selection; benchmark required before production lock-in

## 1. Media transport: WebRTC vs WebSocket

| Criterion | WebRTC | WebSocket |
|---|---|---|
| Primary purpose | Real-time audio/video media | Bidirectional application messages |
| Browser media | Native media APIs | Requires custom audio framing and buffering |
| Jitter handling | RTP media stack | Application responsibility |
| NAT traversal | ICE/STUN/TURN | Usually TCP connectivity |
| Interruption | Track/playback and sender control | Custom chunk cancellation |
| Complexity | Higher operational complexity | Simpler for text/control |
| Decision | Browser microphone/speaker media | Control, transcript, internal events where suitable |

Decision: use WebRTC through LiveKit for browser audio. WebSocket remains available for control messaging; do not conflate the two transports.

## 2. Voice pipeline options

| Option | Strengths | Tradeoffs | Role |
|---|---|---|---|
| Native speech-to-speech realtime model | Natural turn-taking; fewer explicit pipeline stages | Provider/model coupling; transcript/evaluation separation required | MVP candidate |
| STT + LLM + TTS | Modular components, independent provider choice, explicit transcript | Chained latency; orchestration and synchronization burden | Alternative/benchmark |
| LiveKit agent abstraction + provider adapters | Session/media orchestration and provider flexibility | Integration and operational surface | Platform layer |

Decision: start with a native realtime provider behind an application adapter, then benchmark against a chained pipeline using the same question set, devices, regions, and network profiles. Do not assume one provider is always faster or cheaper.

## 3. Backend stack

| Component | Selection | Notes |
|---|---|---|
| Candidate/reviewer frontend | Next.js + React + TypeScript | Responsive, accessible UI |
| Audio processing | WebRTC + Web Audio API + AudioWorklet | Keep heavy processing off UI thread |
| Media gateway | LiveKit | Verify region, TURN, scaling and pricing |
| Control API | Go | Session and policy enforcement |
| AI workers | Python | Evaluation and retrieval |
| Relational store | PostgreSQL | Canonical state |
| Cache/coordination | Redis | Ephemeral data |
| Durable jobs | Redis Streams initially | Revisit broker at scale |
| Vector retrieval | Qdrant | Retrieval projection |
| Object storage | S3-compatible encrypted storage | Audio and artifacts with lifecycle policy |
| Coding editor | Monaco | Language server/compiler integration |
| Sandbox | Isolated ephemeral runner | No network, quotas, resource limits |
| Telemetry | OpenTelemetry + Prometheus + Grafana | Service and model metrics |
| Deployment | Docker; Kubernetes when justified | Avoid premature orchestration overhead |

## 4. Benchmark protocol

### Test profile
Record:
- provider and model version;
- media server region;
- client geography and browser/device;
- input language and speech rate;
- network RTT, jitter, packet loss, and bandwidth;
- prompt/context size;
- session duration;
- date and pricing assumptions.

### Required metrics
- end-of-turn to first audio p50/p95/p99;
- time to first response byte separately;
- barge-in stop latency;
- VAD endpoint errors;
- transcript word error rate against human reference;
- technical evaluation agreement with human reviewers;
- evidence coverage and unsupported-claim rate;
- session failure/reconnect rate;
- cost per completed interview;
- cost per evaluated answer;
- report generation time.

### Experimental controls
- Same scripted answers and question set.
- Same regions and network profiles.
- Randomized provider order.
- Repeated runs and confidence intervals.
- Include Turkish, English, and code-switching samples.
- Use consented/licensed test recordings and human-labelled reference transcripts.
- Publish raw measurement methodology, not only aggregate scores.

## 5. Cost model

Do not hard-code provider prices into the architecture. Capture usage per session:
- inbound audio duration/tokens;
- outbound audio duration/tokens;
- text input/output tokens;
- STT/TTS usage if chained;
- media minutes and TURN egress;
- worker compute;
- object storage and retention;
- vector query and database costs.

Calculate cost per completed interview and per question, including failed/retried sessions. Refresh prices from official provider pricing pages before each budget decision.

## 6. Reference documentation

- OpenAI Realtime: https://developers.openai.com/api/docs/guides/realtime
- OpenAI WebRTC: https://developers.openai.com/api/docs/guides/realtime-webrtc
- LiveKit Agents: https://docs.livekit.io/agents/
- LiveKit deployment: https://docs.livekit.io/transport/self-hosting/
- Qdrant hybrid queries: https://qdrant.tech/documentation/hybrid-queries/
- WebRTC specification: https://www.w3.org/TR/webrtc/
- WebRTC security architecture: https://www.rfc-editor.org/rfc/rfc8827
