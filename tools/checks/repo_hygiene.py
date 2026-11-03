#!/usr/bin/env python3
"""Repository hygiene check (DECISIONS_AND_ROADMAP conventions; ADR-019).

Fails if tracked or untracked-but-not-ignored files contain:
- audio/video files (no real audio samples in Git),
- private keys, cloud/provider tokens, JWTs, or credential assignments whose value is not an
  obvious development placeholder (development secrets must start with "ati-dev-"),
- email addresses outside reserved example domains,
- unexpectedly large files.

Standard library only, so it runs before any dependency is installed.
"""

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
MAX_BYTES = 2 * 1024 * 1024
MEDIA_SUFFIXES = {".wav", ".ogg", ".opus", ".mp3", ".m4a", ".flac", ".webm", ".mp4", ".aac", ".mkv", ".mov"}
SKIP_SCAN_SUFFIXES = {".png", ".jpg", ".jpeg", ".gif", ".ico", ".woff", ".woff2", ".lock"}
SKIP_SCAN_NAMES = {"pnpm-lock.yaml", "uv.lock", "go.sum", "package-lock.json"}
SELF = Path(__file__).resolve()

SECRET_PATTERNS = [
    ("private key", re.compile(r"-----BEGIN (?:RSA |EC |OPENSSH |DSA |ENCRYPTED )?PRIVATE KEY-----")),
    ("AWS access key", re.compile(r"\b(?:AKIA|ASIA)[0-9A-Z]{16}\b")),
    ("OpenAI-style key", re.compile(r"\bsk-(?:proj-|live-)?[A-Za-z0-9_-]{24,}\b")),
    ("GitHub token", re.compile(r"\bgh[pousr]_[A-Za-z0-9]{30,}\b")),
    ("Slack token", re.compile(r"\bxox[abprs]-[A-Za-z0-9-]{10,}\b")),
    ("JWT", re.compile(r"\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}")),
]
ASSIGNMENT = re.compile(
    r"""(?ix)\b([a-z0-9_]*(?:secret|password|passwd|api[_-]?key|token|credential)[a-z0-9_]*)\b
        \s*[:=]\s*["']([^"'\s]{12,})["']"""
)
PLACEHOLDER = re.compile(r"^(ati-dev-|ati_dev_|<|\$\{|\{\{|example|changeme|test-only-|redacted|REDACTED|xxx)", re.I)
EMAIL = re.compile(r"\b[A-Za-z0-9._%+-]+@([A-Za-z0-9.-]+\.[A-Za-z]{2,})\b")
EMAIL_ALLOWED_DOMAINS = re.compile(r"(^|\.)(example\.(com|org|net|invalid)|invalid|test|localhost)$", re.I)


def candidate_files() -> list[Path]:
    out = subprocess.run(
        ["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"],
        cwd=ROOT, check=True, capture_output=True,
    ).stdout.decode()
    return [ROOT / p for p in out.split("\0") if p and (ROOT / p).is_file()]


def scan(path: Path) -> list[str]:
    rel = path.relative_to(ROOT)
    problems: list[str] = []
    if path.suffix.lower() in MEDIA_SUFFIXES:
        return [f"{rel}: media file in repository (real audio/video must never be committed)"]
    if path.stat().st_size > MAX_BYTES:
        problems.append(f"{rel}: larger than {MAX_BYTES} bytes")
    if path.resolve() == SELF or path.suffix.lower() in SKIP_SCAN_SUFFIXES or path.name in SKIP_SCAN_NAMES:
        return problems
    try:
        text = path.read_text(encoding="utf-8")
    except UnicodeDecodeError:
        return problems
    for lineno, line in enumerate(text.splitlines(), 1):
        for label, pattern in SECRET_PATTERNS:
            if pattern.search(line):
                problems.append(f"{rel}:{lineno}: possible {label}")
        for m in ASSIGNMENT.finditer(line):
            if not PLACEHOLDER.match(m.group(2)):
                problems.append(f"{rel}:{lineno}: credential-like assignment '{m.group(1)}' with a non-placeholder value")
        for m in EMAIL.finditer(line):
            if not EMAIL_ALLOWED_DOMAINS.search(m.group(1)):
                problems.append(f"{rel}:{lineno}: email address outside reserved example domains")
    return problems


def main() -> int:
    problems = [p for f in candidate_files() for p in scan(f)]
    for p in problems:
        print(p, file=sys.stderr)
    if problems:
        print(f"repo hygiene: {len(problems)} problem(s)", file=sys.stderr)
        return 1
    print("repo hygiene: ok")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
