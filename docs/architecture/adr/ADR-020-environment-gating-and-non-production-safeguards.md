# ADR-020 — Environment gating and non-production safeguards

**Status:** Proposed — pending architecture review
**Date:** 2026-11-03
**Relates to:** ADR-008, ADR-009; BLUEPRINT §20; THREAT_MODEL_AND_QA §7

## Context

The production gates (evaluation validity, security, reliability, privacy/legal, human governance) and the go-live checklist require human approvals that automation cannot provide. The Phase 1 build has no review or appeal workflow, no legally approved notice, no deletion workflow and an unverified live voice path. Nothing should make it easy to run this build with real candidates by accident.

## Decision

1. `APP_ENV` must be one of `dev`, `test`, `staging`, `production`. Each service **refuses to start** if it is unset or unknown.
2. With `APP_ENV=production`, the control plane refuses to start unless *all* of these hold:
   - A production gate record (`ATI_PRODUCTION_GATES_FILE`) lists each of the five BLUEPRINT §20 gates as approved, with a named human approver and date.
   - The active notice version has `status = approved` and no purpose has basis `to_be_determined_by_legal`.
   - Explicit retention periods are configured for every data category.
   - No secret carries the `ati-dev-` prefix.
   - The database role is neither a superuser nor the owner of the application tables.
   - The agent API URL uses TLS.

   The agent refuses the scripted engine and refuses to start without provider credentials in production.
3. In every environment, interviews cannot be created without explicit retention configuration.
4. Non-production builds identify themselves: the API sends `X-ATI-Environment` on every response, the candidate UI shows a persistent "pre-production build" banner, and demo plans are marked `synthetic`.

## Consequences

Going live requires deliberate, reviewable configuration changes linked to human sign-off. These checks are tripwires, not legal compliance: they make an unsafe deployment fail loudly but cannot prove the gates were genuinely met.

## Verification

Config unit tests cover every refusal condition. The startup smoke test confirms services refuse unknown environments.
