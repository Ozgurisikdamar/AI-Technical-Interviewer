#!/usr/bin/env python3
"""Tier isolation import boundaries for Python services (ADR-001, ADR-011).

Rules (AST-based; standard library only):
- Tier-1 (services/tier1-voice-agent): no database, cache, vector-store or object-storage clients,
  no Tier-2 / evaluation modules, and no database connection settings.
- Tier-2 (any services/tier2-* project, Phase 2+): no LiveKit, no realtime voice provider SDKs,
  no Tier-1 modules.
"""

from __future__ import annotations

import ast
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]

TIER1_FORBIDDEN_MODULES = re.compile(
    r"^(psycopg|psycopg2|psycopg_pool|asyncpg|sqlalchemy|sqlite3|redis|qdrant_client|boto3|botocore|minio|"
    r"tier2_\w*|ati_evaluation\w*|\w*evaluator\w*)(\.|$)"
)
TIER1_FORBIDDEN_STRINGS = re.compile(r"(DATABASE_URL|POSTGRES|PGPASSWORD|REDIS_URL|QDRANT|S3_SECRET)")
TIER2_FORBIDDEN_MODULES = re.compile(r"^(livekit|tier1_voice_agent)(\.|$)")


def python_files(project: Path) -> list[Path]:
    return [p for p in project.rglob("*.py") if ".venv" not in p.parts and "__pycache__" not in p.parts]


def imported_modules(tree: ast.AST) -> list[tuple[int, str]]:
    found = []
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            found.extend((node.lineno, a.name) for a in node.names)
        elif isinstance(node, ast.ImportFrom) and node.module and node.level == 0:
            found.append((node.lineno, node.module))
        elif isinstance(node, ast.Call) and getattr(node.func, "id", getattr(node.func, "attr", "")) in {"import_module", "__import__"}:
            if node.args and isinstance(node.args[0], ast.Constant) and isinstance(node.args[0].value, str):
                found.append((node.lineno, node.args[0].value))
    return found


def check_project(project: Path, forbidden_modules: re.Pattern[str], forbidden_strings: re.Pattern[str] | None) -> list[str]:
    problems = []
    for path in python_files(project):
        rel = path.relative_to(ROOT)
        tree = ast.parse(path.read_text(encoding="utf-8"), filename=str(rel))
        for lineno, module in imported_modules(tree):
            if forbidden_modules.match(module):
                problems.append(f"{rel}:{lineno}: forbidden import '{module}'")
        if forbidden_strings is not None:
            for node in ast.walk(tree):
                if isinstance(node, ast.Constant) and isinstance(node.value, str) and forbidden_strings.search(node.value):
                    problems.append(f"{rel}:{node.lineno}: forbidden setting reference '{node.value}'")
    return problems


def main() -> int:
    problems: list[str] = []
    tier1 = ROOT / "services" / "tier1-voice-agent"
    checked = []
    if tier1.exists():
        checked.append(str(tier1.relative_to(ROOT)))
        problems += check_project(tier1, TIER1_FORBIDDEN_MODULES, TIER1_FORBIDDEN_STRINGS)
    for tier2 in sorted((ROOT / "services").glob("tier2-*")) if (ROOT / "services").exists() else []:
        checked.append(str(tier2.relative_to(ROOT)))
        problems += check_project(tier2, TIER2_FORBIDDEN_MODULES, None)
    for p in problems:
        print(p, file=sys.stderr)
    if problems:
        print(f"import boundaries: {len(problems)} violation(s)", file=sys.stderr)
        return 1
    print(f"import boundaries: ok ({', '.join(checked) or 'no Python services yet'})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
