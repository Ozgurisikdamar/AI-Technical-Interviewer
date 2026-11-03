# Threat Model, Privacy Controls, and QA Test Plan

**Version:** 1.0  
**Status:** Initial security and reliability plan

## 1. Assets

- Candidate identity and contact details.
- Resume and job-description material.
- Raw voice/audio and derived transcript.
- Interview questions and internal rubrics.
- Evaluations, evidence, reports, and appeals.
- Provider credentials and temporary session tokens.
- Human reviewer identities and decisions.
- Audit events and operational telemetry.

## 2. Trust boundaries

1. Candidate device → media gateway.
2. Media gateway → realtime voice provider.
3. Candidate transcript/uploads → prompt orchestration.
4. Retrieved documents → evaluator context.
5. Tier-1 voice agent → interview control API.
6. Durable queue → evaluation workers.
7. Evaluation workers → score aggregation service.
8. Human reviewer UI → decision API.
9. Application → provider and object storage subprocessors.

Treat everything crossing these boundaries as untrusted until authenticated, validated, and authorized.

## 3. Threat scenarios and controls

| Threat | Potential impact | Controls |
|---|---|---|
| Spoken prompt injection | Expose prompt or alter flow | Untrusted input boundary; policy in backend; no privileged tools |
| Resume/JD injection | Evaluator hijack | File parsing isolation; quoted content; prompt separation |
| Malicious retrieved document | Instruction override | Retrieval content treated as evidence only; trusted source allowlist |
| Token theft | Unauthorized session | Short-lived scoped tokens; origin/session binding where supported; rotation |
| Cross-candidate access | Privacy breach | Tenant/candidate authorization on every object |
| Evaluator output tampering | Wrong score | Schema validation, immutable event, service identities |
| Duplicate/replayed events | Double evaluation | Idempotency key, event sequence, deduplication |
| Worker poison message | Queue blockage | Retry cap, DLQ, operator replay |
| Arbitrary code execution | Infrastructure compromise | Isolated sandbox, no network, resource limits, ephemeral filesystem |
| Audio data leakage | Privacy breach | Encryption, access control, retention and deletion |
| False cheating flag | Unfair treatment | Telemetry is not proof; human review; appeal |
| Provider outage | Interrupted interview | Pause/resume, text fallback, retry and status communication |
| Prompt/secret disclosure | Security compromise | No secret in prompts; output filters; least privilege |

## 4. Privacy and governance controls

Before launch:
- Document the purpose and lawful basis for every data category.
- Provide candidate-facing disclosures in accessible language.
- Determine whether consent is appropriate/required for each processing purpose; do not assume a generic checkbox resolves all legal issues.
- Document data processors, subprocessors, hosting regions, and international transfers.
- Define retention for audio, transcript, logs, reports, and appeals separately.
- Provide access, correction, deletion, and export workflows as applicable.
- Avoid collecting audio when text mode is sufficient and chosen.
- Offer reasonable accessibility accommodations.
- Prohibit emotion, personality, accent, and demographic inference.
- Conduct legal review for KVKK, GDPR where relevant, and employment-specific regulation.
- Establish security incident and data-breach response procedures.

## 5. QA matrix

### Audio and network
- Microphone permission denied.
- No microphone device.
- Device removed mid-session.
- Browser tab suspended.
- 1%, 5%, and 10% packet loss.
- High jitter and bandwidth throttling.
- TURN relay path.
- Reconnect after temporary disconnect.
- Candidate speaks while AI output is active.
- Candidate silence vs packet loss distinction.
- Multiple supported browsers and devices.

### State and data integrity
- Duplicate answer event.
- Duplicate evaluation job.
- Out-of-order transcript segments.
- Missing transcript segment.
- Transcript revision after evaluation.
- Concurrent state transition requests.
- Session pause/resume race.
- Worker restart during processing.
- Database timeout and recovery.
- Queue consumer crash and reclaim.
- Qdrant unavailability and canonical fallback.

### AI and prompt security
- Spoken "ignore all previous instructions".
- Ask for system prompt or hidden rubric.
- Attempt to change score.
- Prompt injection in resume.
- Prompt injection in job description.
- Instruction embedded in retrieved reference.
- Tool invocation without authorization.
- Malformed evaluator JSON.
- Fabricated evidence timestamps.
- Unsupported technical claims.
- Contradictory answer segments.
- Evaluator disagreement and review routing.

### Coding sandbox
- Infinite loop.
- Memory exhaustion.
- Process fork/spawn attempt.
- Filesystem traversal.
- Network access attempt.
- Huge stdout/stderr.
- Malformed source and compiler crash.
- Time-limit enforcement.
- Cleanup after execution.
- Tenant isolation.

### Fairness and accessibility
- Turkish and English interview sessions.
- Code-switching.
- Varied accents and speech rates.
- Speech disability accommodations.
- Text-only alternative.
- Long thinking pauses.
- Low-quality microphone.
- Screen reader and keyboard navigation.
- No scoring based on accent or vocal traits.
- Human review of disputed assessment.

## 6. Production observability

Required measurements:
- end-of-turn-to-first-audio p50/p95/p99;
- barge-in stop latency p50/p95/p99;
- VAD false-positive and false-negative rates;
- transcript WER on consented labelled evaluation sets;
- evaluation duration and retry rate;
- schema validation failure rate;
- evidence coverage;
- evaluator disagreement;
- human review and appeal rates;
- session completion and reconnect success;
- cost per completed interview.

Use OpenTelemetry trace correlation: trace_id, session_id, question_id, turn_id, evaluation_id. Apply data minimization and PII redaction.

## 7. Go-live checklist

- [ ] Threat model reviewed and owners assigned.
- [ ] Provider contracts and data residency reviewed.
- [ ] Candidate disclosure and lawful basis approved.
- [ ] Retention/deletion workflows tested.
- [ ] Secrets manager configured; no secrets in Git/client.
- [ ] RBAC and cross-candidate isolation tested.
- [ ] Prompt injection suite passes.
- [ ] Code sandbox isolation verified.
- [ ] Realtime latency benchmark completed by region/device/network.
- [ ] Barge-in and reconnect chaos tests pass.
- [ ] Evaluation benchmark reviewed by qualified humans.
- [ ] Confidence calibration status explicitly documented.
- [ ] Human review and appeal path operational.
- [ ] Accessibility alternative available.
- [ ] Incident response and rollback runbooks approved.
