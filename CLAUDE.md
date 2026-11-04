# Repository rules for AI assistants

- **Do not use GitHub Actions.** Never add, restore or depend on files under `.github/workflows/` or
  `.github/actions/`, and do not report Actions runs as evidence. `make checks` fails if such files exist.
- Verify changes locally: `make test` (hygiene, contracts, Go, agent and web suites) and `make e2e` (local stack,
  media and browser end-to-end tests). Report what was run and its result.
- The repository conventions, including this rule, are in
  [docs/architecture/DECISIONS_AND_ROADMAP.md](docs/architecture/DECISIONS_AND_ROADMAP.md#repository-implementation-conventions).
  Implementation status and blockers are tracked in [docs/status/IMPLEMENTATION_STATUS.md](docs/status/IMPLEMENTATION_STATUS.md).
