# ADR-019 — Repository layout and contract locations

**Status:** Proposed — pending architecture review
**Date:** 2026-11-03
**Relates to:** ADR-002; DECISIONS_AND_ROADMAP "Repository implementation conventions"

## Context

The conventions say API contracts belong in `docs/contracts/` once endpoints exist, and that prompts and JSON Schemas are "versioned alongside tests". Schemas, prompts and the state machine are consumed by Go and Python at build and test time, and the Go module cannot embed files outside its own directory.

## Decision

1. `contracts/` (repository root) holds the machine-readable contracts and their tests:
   - `state-machine/`, `schemas/<name>/v<N>/`, `prompts/<name>/v<N>/` with a sha256 `manifest.json`, `content/` (plans, notices), `fixtures/`, `tests/` (pytest), `tools/`.
2. `docs/contracts/openapi/` holds the HTTP API contract, per the convention.
3. Services keep their own build roots: `services/control-plane/` (Go module), `services/tier1-voice-agent/` (uv project), `apps/web/` (pnpm project). The Go service embeds a copy of the contracts it needs. A test fails if that copy differs from `contracts/`.
4. Diagrams are Mermaid source. The state diagram is generated from the JSON contract and drift-tested.
5. `tools/checks/` holds dependency-free repository checks: secrets, PII and audio-file scan, and Python import boundaries. `tools/dev/` holds local stack scripts.
6. Nothing derived from candidates, and no real audio, secrets or credentials, is committed. Development-only placeholder secrets carry the prefix `ati-dev-` so scanners can recognise them, and production mode refuses them (ADR-020).

## Consequences

One source of truth per contract, with mechanical drift detection. A detailed map is in [DIRECTORY_MAP.md](../DIRECTORY_MAP.md).
