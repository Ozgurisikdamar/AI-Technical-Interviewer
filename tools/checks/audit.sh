#!/usr/bin/env bash
# Dependency vulnerability scans, run locally (the repository uses no GitHub Actions; CLAUDE.md).
# They query vulnerability databases (vuln.go.dev, PyPI, the npm registry), so they need network
# access and are not part of `make test`. Every lockfile in the repository is scanned.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
GOVULNCHECK_VERSION=v1.9.0
PIP_AUDIT_VERSION=2.9.0
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
failed=()

echo "== Go: services/control-plane (govulncheck $GOVULNCHECK_VERSION, reachable symbols)"
(
  cd "$ROOT/services/control-plane"
  # Build the scanner with the module's toolchain: it must understand the module's Go version.
  GOTOOLCHAIN="$(sed -n 's/^toolchain //p' go.mod)" go run "golang.org/x/vuln/cmd/govulncheck@$GOVULNCHECK_VERSION" ./...
) || failed+=("services/control-plane")

while IFS= read -r lock; do
  dir="$(dirname "$lock")"
  rel="${dir#"$ROOT"/}"
  echo "== Python: $rel (pip-audit $PIP_AUDIT_VERSION, hashed lockfile export)"
  (cd "$dir" && uv export --frozen --no-emit-project --format requirements-txt -o "$tmp/requirements.txt" >/dev/null) || { failed+=("$rel"); continue; }
  uvx --from "pip-audit==$PIP_AUDIT_VERSION" pip-audit -r "$tmp/requirements.txt" --disable-pip --progress-spinner off || failed+=("$rel")
done < <(find "$ROOT/services" "$ROOT/contracts" "$ROOT/tests" -maxdepth 3 -name uv.lock -not -path '*/.venv/*' | sort)

echo "== JavaScript: apps/web (pnpm audit, production dependencies)"
(cd "$ROOT/apps/web" && pnpm audit --prod) || failed+=("apps/web")

if ((${#failed[@]})); then
  echo "audit: FAILED for ${failed[*]}"
  exit 1
fi
echo "audit: ok (no known vulnerabilities reachable from the code or listed for the locked dependencies)"
