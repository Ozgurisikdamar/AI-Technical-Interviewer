#!/usr/bin/env bash
# Copy the contracts the Tier-1 agent ships as package data. The agent's drift test fails if the
# copies differ from contracts/ (ADR-019).
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEST="$ROOT/services/tier1-voice-agent/src/tier1_voice_agent/contracts"
mkdir -p "$DEST"
cp "$ROOT/contracts/prompts/tier1-interviewer/v1/system.txt" "$DEST/tier1-interviewer.v1.txt"
cp "$ROOT/contracts/prompts/manifest.json" "$DEST/prompts-manifest.json"
cp "$ROOT/contracts/schemas/tier1/v1/tier1-context.schema.json" "$DEST/tier1-context.v1.schema.json"
cp "$ROOT/contracts/schemas/transcript/v1/segment-submission.schema.json" "$DEST/segment-submission.v1.schema.json"
touch "$DEST/__init__.py"
echo "synced agent contracts into $DEST"
