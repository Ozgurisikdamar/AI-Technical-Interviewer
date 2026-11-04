# Tier-2 evaluator

The post-interview evaluation worker (ADR-001, ADR-022). Once an interview is completed it evaluates each answer
against the answer's versioned rubric, cites transcript evidence, and reports every attempt to the control plane,
which validates the attempt before anything becomes an evaluation. Every interview then goes to human review. The
worker never decides on hiring and is never in the live interview.

## Pipeline

1. The control plane's outbox relay publishes `interview.completed` to the Redis stream `ati:tier2:events`,
   after applying T14 (`EVALUATION_PENDING`). The entry carries identifiers only.
2. The worker reads the stream with the consumer group `tier2`. An entry is acknowledged after handling.
   - A failed entry stays pending and is reclaimed once it has been idle for the visibility timeout (`XAUTOCLAIM`).
   - While an entry is being handled, its idle time is reset (`XCLAIM … JUSTID`), so that a slow evaluation is not
     reclaimed by another worker.
   - After `ATI_TIER2_MAX_DELIVERIES` deliveries, or at once for a malformed entry, the entry moves to
     `ati:tier2:dlq`. The interview is then routed to human review with `failure_reason: dead_lettered`. The report
     to the control plane comes first; if that report fails, the entry stays pending and the fallback is not lost.
   - `answer.finalized` entries are acknowledged without work: Tier-2 never acts on a live interview (INV-6).
3. The interview's evidence is read in one read-only snapshot, as `ati_tier2_evaluator`. That role can read answers,
   segments, questions, rubrics and two narrow views. It sees no candidate identity and can write nothing.
4. Each answer is evaluated, or the evaluator abstains, or the answer is skipped:
   - abstains on synthetic test speech (`synthetic_source`);
   - abstains when no candidate speech is evidence-eligible (`no_candidate_speech`);
   - abstains when mean ASR confidence is below 0.6, an uncalibrated policy value (`transcript_quality_low`);
   - skips answers without a stored rubric; the reviewer sees `no_rubric` or `not_evaluated`;
   - skips answers this evaluator version already evaluated (redelivery).
5. Each attempt goes to `POST /internal/v1/tier2/sessions/{id}/evaluations`, with the Tier-2 service token.
   - The control plane stores it raw.
   - It validates schema, identity, arithmetic, verbatim quotes, spans and references (`internal/evaluation`).
   - A valid attempt becomes the canonical evaluation.
6. `POST …/complete` routes the interview to human review (T15).

## Evaluators

| `ATI_TIER2_EVALUATOR` | What it is | Status |
|---|---|---|
| `reference` | Deterministic. It matches a synthetic rubric's indicator phrases in the candidate's words and quotes them verbatim. It does not assess correctness, works only on rubrics marked synthetic, and is refused in production. | Used by tests and the local E2E stack |
| `openai` | Responses API with Structured Outputs (`strict`), `store: false`, text only | **Not verified** against the live provider (no credentials here) |
| `anthropic` | Messages API, streamed, structured outputs (`output_config.format`) and `effort: high`, text only. Server-side refusal fallbacks are deliberately off: they would change the model that produced an evaluation. | **Not verified** against the live provider (no credentials here) |

- **Model IDs.** They have no defaults and are checked against the model registry
  (`contracts/models/model-registry.v1.json`, purpose `tier2_evaluator`). Production accepts only `verified`
  models; none is verified yet, so production refuses to start with a model evaluator.
- **Endpoints and headers.** The endpoint is pinned (`ATI_TIER2_BASE_URL`, defaulting to the provider's API). An
  inherited `OPENAI_BASE_URL` or `ANTHROPIC_BASE_URL` that differs from it is refused, and so is
  `ANTHROPIC_CUSTOM_HEADERS`.
- **Prompt.** The system instructions are the pinned `tier2-evaluator` v1 template plus the output rules in
  `prompt.py`. The answer's data travels in the user turn as one delimited JSON block. Synthetic indicator lists are
  never sent to a model.
- **Versions.** A model evaluator's version is `<provider>:<model>@p<first 12 hex of the instructions' sha256>`, and
  the full hash is stored as `prompt_sha256`. Changing the template or the output rules therefore creates a new
  evaluation (INV-2).
- **Provider errors.** Overload and server errors (429, 5xx, 529, error events mid-stream) leave the entry pending
  for retry. Other provider errors, refusals and truncated output are recorded as `failed` attempts, and the answer
  goes to review without an AI draft.

## Configuration

| Variable | Notes |
|---|---|
| `APP_ENV` | `dev`, `test`, `staging` or `production` |
| `ATI_TIER2_EVALUATOR` | `reference`, `openai` or `anthropic` |
| `ATI_TIER2_MODEL`, `OPENAI_API_KEY` / `ANTHROPIC_API_KEY`, `ATI_TIER2_BASE_URL` | Model evaluators only |
| `ATI_REDIS_URL` | `rediss://` in production |
| `ATI_TIER2_DATABASE_URL` | Connects as `ati_tier2_evaluator`; production needs `sslmode=verify-full` |
| `ATI_CONTROL_PLANE_INTERNAL_URL`, `ATI_TIER2_SERVICE_TOKEN` | The control plane stores only the token's sha256 (`ATI_TIER2_SERVICE_TOKEN_SHA256`) |
| `ATI_TIER2_CONSUMER` | Consumer name in the group (default `tier2-1`) |
| `ATI_TIER2_MAX_DELIVERIES` | Default 5 |
| `ATI_TIER2_VISIBILITY_TIMEOUT_MS` | Default 120000 |

## Development

```bash
make tier2-test
```

Runs ruff, mypy (strict) and pytest. The consumer tests start a throwaway local `redis-server`. The provider
adapters are tested against recorded-shape responses only.

```bash
tools/dev/sync-tier2-contracts.sh
```

Refreshes the packaged copies of the prompt, schemas and model registry; a drift test compares them with `contracts/`.

## Offline benchmark

```bash
make tier2-bench
```

This runs the harness of [DATA_AND_ML_LIFECYCLE.md](../../docs/architecture/DATA_AND_ML_LIFECYCLE.md), `python -m tier2_evaluator.bench`:
1. It evaluates every item of a versioned dataset (`bench/synthetic-v1`) with the reference evaluator.
2. It validates every attempt with the control plane's own validator (`ati-admin validate-evaluations`).
3. It reports:
   - evidence validity and abstention;
   - per-dimension agreement with the reference labels (MAE, share within 10 points, quadratic-weighted kappa on
     20-point bands), next to agreement between raters (kappa, and Krippendorff's alpha with the interval metric);
   - latency and tokens.

The dataset's labels are synthetic, so the run checks the harness and the validator, not evaluation quality.
`--require-valid` fails the run if the validator rejects any attempt.

## Not done yet

- No live-provider verification and no benchmark against human labels (Milestone C). Agreement with reviewers is
  unknown, which is why every interview is reviewed.
- No health endpoint or metrics exporter. The worker logs each attempt's outcome, and Redis shows pending and
  dead-lettered entries.
- One entry is handled at a time. Throughput is bounded by the provider latency per answer.
