# Changelog

All notable changes to this project are documented in this file. The format
is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
While the major version is 0, a minor release may break the API.

## [Unreleased]

Preparation for the first public release.

### Removed

- **Breaking:** the test-only clock injection API: `Clock`, `EngineOption`,
  `WithClock`, `ManagerClock`, `WithManagerClock` and `ErrNilClock`.
  `NewEngine` now takes only an adapter and a policy; existing two-argument
  calls compile unchanged. Engine retries and manager requeues use standard
  library timers; test code built on this library should use
  `testing/synctest` for virtual time (ADR-0010).
- The unused `internal/conformance` package (never part of the public API).

### Changed

- **Breaking:** the minimum Go version is now 1.25 (was 1.22), because the
  test suite uses `testing/synctest`. CI tests Go 1.25, `oldstable` and
  `stable` (ADR-0011).
- Time- and concurrency-dependent tests run on `testing/synctest` virtual
  time instead of fake clocks, polling and wall-clock timeouts.
- CI no longer needs private-module credentials; every action is pinned to
  a commit SHA, a golangci-lint job was added, and the pre-commit step (with
  its Python setup) was dropped from CI (ADR-0013).
- Dependabot keeps Go modules and GitHub Actions current.

### Added

- Package documentation and doc comments for every exported identifier.
- `ExampleEngine_Reconcile`, a complete adapter example.
- `NOTICE`, `AGENTS.md`, and `docs/` with a retrospective PRD and ADRs.

## [0.2.0] - 2026-07-17

- Add generic desired, observed, condition, action, undo, and result contracts.
- Validate arbitrary action dependency DAGs and produce deterministic
  topological orders, including fan-out and branch convergence.
- Add an observe-plan-apply-verify engine with classified retries, bounded
  backoff, attempt deadlines, dependency-aware rollback, and status events.
- Add a bounded keyed manager with generation coalescing, supersession,
  delayed requeues, future results, cloned pending snapshots, and graceful
  shutdown.
- Add bounded non-blocking status streams with explicit dropped-event markers.
- Add lifecycle-compatible export and adoption of pending desired state and
  absolute retry deadlines.
- Use canonical, integrity-checked, versioned artifacts without carrying
  planned actions, running work, resource rights, or runtime errors.
- Define codec buffer/value ownership and add compile-checked DAG, manager, and
  handoff examples.
- Support Go 1.22 and test Go 1.22 through Go 1.26 in GitHub Actions.
- Require `go-concurrency` and `go-lifecycle` at `v0.2.0`.

[Unreleased]: https://github.com/justusschock/go-reconciliation/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/justusschock/go-reconciliation/releases/tag/v0.2.0
