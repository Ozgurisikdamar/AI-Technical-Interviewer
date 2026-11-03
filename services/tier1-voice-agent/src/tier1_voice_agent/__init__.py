"""Tier-1 realtime interviewer.

Conducts the interview according to the control plane's flow engine. It never scores, ranks or
judges answers, has no database access and no Tier-2 code (ADR-001, ADR-011).
"""

__all__ = ["__version__"]
__version__ = "0.1.0"
