# Top-level entry points. Each service also works on its own; see its README.
SHELL := /bin/bash
.DEFAULT_GOAL := help
CP := services/control-plane
STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.8.2

.PHONY: help checks contracts-test sync-contracts go-lint go-test test

help:
	@echo "make checks          repository hygiene, Python import boundaries, generated diagram"
	@echo "make contracts-test  contract, drift, isolation and OpenAPI tests"
	@echo "make sync-contracts  copy contracts embedded by the Go control plane"
	@echo "make go-lint         go vet + staticcheck for the control plane"
	@echo "make go-test         control-plane tests (race detector)"
	@echo "make test            everything available"

checks:
	python3 tools/checks/repo_hygiene.py
	python3 tools/checks/python_import_boundaries.py
	python3 contracts/tools/generate_state_diagram.py --check

contracts-test:
	cd contracts && uv run --frozen pytest -q

sync-contracts:
	tools/dev/sync-go-contracts.sh

go-lint:
	cd $(CP) && test -z "$$(gofmt -l .)" || (gofmt -l . && echo "gofmt needed" && exit 1)
	cd $(CP) && go vet ./... && go run $(STATICCHECK) ./...

go-test:
	cd $(CP) && go test -race -count=1 ./...

test: checks contracts-test go-lint go-test
