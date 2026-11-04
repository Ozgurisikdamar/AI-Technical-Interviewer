"""Entry point: python -m tier1_voice_agent"""

from __future__ import annotations

import asyncio
import logging
import os
import sys

from . import config, logging_setup, prompt


def main() -> None:
    # The interviewer uses its own VAD or the provider's server VAD; never load (or fetch) the
    # framework's local turn-detection weights.
    os.environ.setdefault("LIVEKIT_AGENTS_PRELOAD_LOCAL_INFERENCE", "0")
    try:
        cfg = config.load(os.environ)
        prompt.system_prompt()  # verifies the sha256 pin before serving anything
    except (config.ConfigError, prompt.PromptIntegrityError) as e:
        print(f'{{"level":"ERROR","msg":"agent refused to start","reason":"{e}"}}', file=sys.stderr)
        raise SystemExit(1) from None
    logging_setup.setup(os.environ.get("ATI_LOG_LEVEL", "INFO"))
    for warning in cfg.warnings:
        logging.getLogger("tier1.config").warning(warning)
    from .worker import build_server

    server = build_server(cfg)
    asyncio.run(server.run(devmode=False))


if __name__ == "__main__":
    main()
