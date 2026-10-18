# Changelog

All notable changes to this project are documented in this file. The format
is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
While the major version is 0, a minor release may break the API.

## [Unreleased]

Preparation for the first public release.

### Changed

- **Breaking:** the minimum Go version is now 1.25 (was 1.22), because the
  test suite uses `testing/synctest`. CI tests Go 1.25, `oldstable` and
  `stable` (ADR-0011).
- Time- and concurrency-dependent tests run on `testing/synctest` virtual
  time instead of fake clocks, polling and wall-clock timeouts. The public
  clock options (`Clock`, `EngineOption`, `WithClock`, `ManagerClock`,
  `WithManagerClock`, `ErrNilClock`) are unchanged and keep dedicated tests
  (ADR-0010).
- CI no longer uses a token secret to fetch dependencies; every action is
  pinned to a commit SHA, a golangci-lint job was added, and the pre-commit
  step (with its Python setup) was dropped from CI (ADR-0013).
- Dependabot keeps Go modules and GitHub Actions current.

### Added

- Package documentation and doc comments for every exported identifier.
- A unit test for the internal `conformance.Adapter` test helper.
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
- Require `concurrency` and `lifecycle` at `v0.2.0`.

[Unreleased]: https://github.com/justusschock/reconciliation/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/justusschock/reconciliation/releases/tag/v0.2.0
