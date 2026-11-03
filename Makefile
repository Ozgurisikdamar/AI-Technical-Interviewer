# Top-level entry points. Each service also works on its own; see its README.
SHELL := /bin/bash
.DEFAULT_GOAL := help

.PHONY: help checks contracts-test test

help:
	@echo "make checks          repository hygiene and Python import boundaries"
	@echo "make contracts-test  contract, drift, isolation and OpenAPI tests"
	@echo "make test            everything available"

checks:
	python3 tools/checks/repo_hygiene.py
	python3 tools/checks/python_import_boundaries.py
	python3 contracts/tools/generate_state_diagram.py --check

contracts-test:
	cd contracts && uv run --frozen pytest -q

test: checks contracts-test
