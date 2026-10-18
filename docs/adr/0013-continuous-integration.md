# ADR 0013: Continuous integration and local hooks

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-10-18 |
| Deciders | Justus Perillieux |
| Supersedes | — |
| Related | PRD-0001, ADR-0011, ADR-0012 |

## Context

Recorded retrospectively. Before the public release, CI fetched
dependencies with a repository token secret, used actions by mutable tags,
and ran `pre-commit` through `actions/setup-python` and
`pip install pre-commit`.
The pre-commit configuration runs `gofmt`, `go vet` and `go test` plus
generic hygiene hooks (YAML syntax, merge markers, large files, private keys,
whitespace) fetched from `pre-commit-hooks` by tag at run time.

## Decision drivers

- D1: Least privilege and no secrets in a public repository's CI.
- D2: Reproducible, tamper-resistant action and tool versions.
- D3: No duplicated checks.
- D4: Runnable locally with `act`.

## Considered options

1. **Pinned Go-only pipeline: test matrix and golangci-lint; pre-commit local only**
   - Pro: every action is SHA-pinned; formatting, vet, tests and lint are
     covered once; no Python toolchain in CI.
   - Con: the generic hygiene hooks no longer run in CI.
2. **Keep pre-commit in CI**
   - Pro: hygiene hooks keep running.
   - Con: duplicates the Go checks; downloads hook repositories by mutable
     tag and an unpinned `pre-commit` from PyPI, outside the pinning policy.
3. **Keep the token-based workflow**
   - Not needed once dependencies are public, and a secret in CI widens the
     attack surface.

## Decision

We will run CI on `push` to `main` and tags and on `pull_request` (never
`pull_request_target` or `workflow_run`), with `permissions: contents:
read`, every action pinned to a commit SHA, `persist-credentials: false`, a
timeout on every job and cancellation of superseded runs. The `test` job
runs `go mod verify`, `go mod tidy -diff`, `go vet` and `go test -race` on Go
`1.25.x`, `oldstable` and `stable`; the `lint` job runs golangci-lint
v2.15.0 with the configuration in `.golangci.yml`. `actions/setup-python` and
the pre-commit step are dropped; `.pre-commit-config.yaml` stays as optional
local tooling. Dependabot updates Go modules and actions weekly.
`TestCIWorkflowPolicy` enforces these invariants.

## Consequences

### Positive
- No secrets, minimal permissions, reproducible actions.

### Negative
- YAML and whitespace hygiene rely on local hooks and review.

### Follow-ups
- Pin `pre-commit-hooks` to a commit SHA if pre-commit is reintroduced in CI.

## Links

- `.github/workflows/ci.yml`, `.github/dependabot.yml`, `.golangci.yml`,
  `.pre-commit-config.yaml`
