#!/usr/bin/env bash
# Copy the contracts the Tier-2 evaluator ships as package data. Its drift test fails if the copies
# differ from contracts/ (ADR-019). Nothing Tier-1-specific is copied (ADR-001).
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEST="$ROOT/services/tier2-evaluator/src/tier2_evaluator/contracts"
mkdir -p "$DEST"
cp "$ROOT/contracts/prompts/tier2-evaluator/v1/system.txt" "$DEST/tier2-evaluator.v1.txt"
cp "$ROOT/contracts/prompts/manifest.json" "$DEST/prompts-manifest.json"
cp "$ROOT/contracts/schemas/evaluation/v1/evaluation.schema.json" "$DEST/evaluation.v1.schema.json"
cp "$ROOT/contracts/schemas/rubric/v1/rubric.schema.json" "$DEST/rubric.v1.schema.json"
cp "$ROOT/contracts/schemas/events/v1/interview-completed.schema.json" "$DEST/interview-completed.v1.schema.json"
cp "$ROOT/contracts/models/model-registry.v1.json" "$DEST/model-registry.v1.json"
touch "$DEST/__init__.py"
echo "synced Tier-2 contracts into $DEST"
