"""Generate the Mermaid state diagram from the machine-readable state-machine contract.

Usage:
    python3 contracts/tools/generate_state_diagram.py          # write the .mmd file
    python3 contracts/tools/generate_state_diagram.py --check  # exit 1 if the file is stale

The generated diagram, not a hand-drawn one, is the reviewable picture of contract v1.2.
Transitions that extend the BLUEPRINT diagram are labelled with their ADR.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
CONTRACT = ROOT / "state-machine" / "interview-session.v1.2.json"
OUTPUT = ROOT / "state-machine" / "interview-session.v1.2.mmd"


def render(contract: dict) -> str:
    lines = [
        "%% Generated from interview-session.v1.2.json by contracts/tools/generate_state_diagram.py.",
        "%% Do not edit by hand.",
        "stateDiagram-v2",
        f"  [*] --> {contract['initial_state']}",
    ]
    for t in contract["transitions"]:
        label = t["event"]
        if t["source"] != "diagram":
            label += f" ({t['source']})"
        if t["phase"] > 1:
            label += f" [phase {t['phase']}]"
        lines.append(f"  {t['from']} --> {t['to']}: {label}")
    for s in contract["states"]:
        if s["terminal"]:
            lines.append(f"  {s['name']} --> [*]")
    return "\n".join(lines) + "\n"


def main(argv: list[str]) -> int:
    contract = json.loads(CONTRACT.read_text(encoding="utf-8"))
    rendered = render(contract)
    if "--check" in argv:
        current = OUTPUT.read_text(encoding="utf-8") if OUTPUT.exists() else ""
        if current != rendered:
            print(f"{OUTPUT} is stale; run contracts/tools/generate_state_diagram.py", file=sys.stderr)
            return 1
        return 0
    OUTPUT.write_text(rendered, encoding="utf-8")
    print(f"wrote {OUTPUT}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
