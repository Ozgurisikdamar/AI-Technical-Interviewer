# Verification evidence

Machine-written records of checks that cannot run in `make test`, kept so a reviewer can trace a claim
such as "model X is verified" (ADR-021) to a dated run.

| File | Written by | Holds |
|---|---|---|
| `provider-smoke-<date>.json` | `make provider-smoke` | Model IDs, request, session and response IDs, tool-call outcomes, output-monitor flag names, transcription word-overlap scores, latency percentiles, errors, limitations |

Evidence files never hold credentials, prompts, transcripts or audio; the harness's tests check this.
A model's status in `contracts/models/model-registry.v1.json` changes to `verified` only through a
reviewed change that points at such a file. No live run has been recorded yet: this repository has
no provider credentials.
