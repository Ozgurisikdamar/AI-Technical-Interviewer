#!/usr/bin/env bash
# Copy the contracts the control plane embeds (Go cannot embed files outside its module).
# A Go drift test fails if the copies differ from contracts/ (ADR-019).
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEST="$ROOT/services/control-plane/internal/contracts/embedded"
FILES=(
  state-machine/interview-session.v1.1.json
  schemas/tier1/v1/tier1-context.schema.json
  schemas/transcript/v1/segment-submission.schema.json
  schemas/transcript/v1/transcript-segment.schema.json
  schemas/events/v1/answer-finalized.schema.json
  schemas/events/v1/interview-completed.schema.json
  schemas/plan/v1/interview-plan.schema.json
  schemas/consent/v1/consent-notice.schema.json
)
rm -rf "$DEST"
for f in "${FILES[@]}"; do
  mkdir -p "$DEST/$(dirname "$f")"
  cp "$ROOT/contracts/$f" "$DEST/$f"
done
for d in content/plans content/notices; do
  mkdir -p "$DEST/$d"
  cp "$ROOT/contracts/$d/"*.json "$DEST/$d/"
done
echo "synced $(find "$DEST" -type f | wc -l) contract files into $DEST"
