"""Entry point: python -m tier2_evaluator"""

from __future__ import annotations

import asyncio
import json
import logging
import os
import sys

from . import config, logging_setup, prompt


def main() -> None:
    try:
        cfg = config.load(os.environ)
        prompt.template()  # verifies the sha256 pin before anything is evaluated
    except (config.ConfigError, prompt.PromptIntegrityError) as e:
        print(json.dumps({"level": "ERROR", "msg": "tier-2 worker refused to start", "reason": str(e)}), file=sys.stderr)
        raise SystemExit(1) from None
    logging_setup.setup(os.environ.get("ATI_LOG_LEVEL", "INFO"))
    for warning in cfg.warnings:
        logging.getLogger("tier2.config").warning(warning)
    from .worker import run

    asyncio.run(run(cfg))


if __name__ == "__main__":
    main()
