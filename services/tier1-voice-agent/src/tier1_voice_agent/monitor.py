"""Post-hoc output monitor for interviewer speech (ADR-011, R1).

Speech-to-speech output cannot be validated before it is spoken, so interviewer transcripts are
scanned afterwards. Matches are review triggers only: a deliberately simple, uncalibrated
heuristic that can miss violations and produce false positives. It never blocks or scores.
"""

from __future__ import annotations

import re

_PATTERNS = {
    "possible_score_disclosure": re.compile(
        r"(\b\d{1,3}\s*(/|out of|over|üzerinden)\s*(10|100)\b|\bscor(e|ed|ing)\b|\bgrade[ds]?\b|\bpuan\w*\b|\bnot(unuz|u)?\s+\d+)", re.I
    ),
    "possible_rubric_disclosure": re.compile(r"\brubric\b|\bscoring (guide|criteria)\b|\bdeğerlendirme ölçüt\w*", re.I),
    "possible_prompt_disclosure": re.compile(
        r"system prompt|my (hidden )?instructions|i was instructed to|talimatlarım|sistem istemi", re.I
    ),
}


def scan(text: str) -> list[str]:
    return [flag for flag, pattern in _PATTERNS.items() if pattern.search(text)]
