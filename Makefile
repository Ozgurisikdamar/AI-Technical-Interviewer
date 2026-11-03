# Top-level entry points. Each service also works on its own; see its README.
SHELL := /bin/bash
.DEFAULT_GOAL := help
CP := services/control-plane
AGENT := services/tier1-voice-agent
STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.8.2

.PHONY: help checks contracts-test sync-contracts go-lint go-test agent-test pg-start pg-stop test

help:
	@echo "make checks          repository hygiene, Python import boundaries, generated diagram"
	@echo "make contracts-test  contract, drift, isolation and OpenAPI tests"
	@echo "make sync-contracts  copy contracts embedded by the Go control plane"
	@echo "make go-lint         go vet + staticcheck for the control plane"
	@echo "make go-test         control-plane tests (race detector; starts a local PostgreSQL)"
	@echo "make agent-test      Tier-1 agent: ruff, mypy (strict), pytest"
	@echo "make pg-start|pg-stop ephemeral PostgreSQL 16 for tests"
	@echo "make test            everything available"

checks:
	python3 tools/checks/repo_hygiene.py
	python3 tools/checks/python_import_boundaries.py
	python3 contracts/tools/generate_state_diagram.py --check

contracts-test:
	cd contracts && uv run --frozen pytest -q

sync-contracts:
	tools/dev/sync-go-contracts.sh
	tools/dev/sync-agent-contracts.sh

go-lint:
	cd $(CP) && test -z "$$(gofmt -l .)" || (gofmt -l . && echo "gofmt needed" && exit 1)
	cd $(CP) && go vet ./... && go run $(STATICCHECK) ./...

pg-start:
	tools/dev/pg.sh start

pg-stop:
	tools/dev/pg.sh stop

go-test:
	@if [ -z "$$ATI_TEST_PG_ADMIN_URL" ]; then tools/dev/pg.sh start >/dev/null; fi
	cd $(CP) && go test -race -count=1 ./...

agent-test:
	cd $(AGENT) && uv run --frozen ruff check src tests && uv run --frozen ruff format --check src tests
	cd $(AGENT) && uv run --frozen mypy
	cd $(AGENT) && uv run --frozen pytest -q

test: checks contracts-test go-lint go-test agent-test
