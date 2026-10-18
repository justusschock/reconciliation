# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
While the major version is `0`, a minor release may break the API.

## [Unreleased]

## [0.1.0] - 2026-10-18

First public release.

### Added

- Action graphs: `Plan[A]` actions declare dependencies; `ValidatePlan`
  and `Ordered` reject invalid graphs and produce a stable topological
  order.
- `Engine[S, O, A, U]` (`NewEngine`): observes, plans, validates the whole
  plan before mutating, applies through a domain `Adapter`, and verifies
  with a fresh observation.
- Classified retries: errors wrapped with `Transient` or `Conflict` are
  retried with capped, optionally jittered exponential backoff per
  `Policy`; `Permanent` and unclassified errors are not.
- Dependency-safe rollback of completed reversible actions in reverse
  order, under a bounded cleanup deadline.
- `Manager[S, O]` (`NewManager`): one call per key at a time, a bound on
  active keys, generation coalescing and supersession (`ErrSuperseded`),
  delayed requeues and graceful shutdown.
- Pluggable time sources: `Clock` with `WithClock` and `ManagerClock` with
  `WithManagerClock`.
- Non-blocking status events through `WithStatusSink`,
  `WithManagerStatusSink` and `EventStream`, with `StatusDropped` for
  lagging subscribers.
- Package `handoff`: `NewExporter` and `NewAdopter` move pending desired
  state and absolute retry deadlines between processes as
  integrity-checked `lifecycle` artifacts.
- Requirements and architecture decision records under `docs/`.

Requires Go 1.25 or newer.

[Unreleased]: https://github.com/justusschock/reconciliation/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/justusschock/reconciliation/releases/tag/v0.1.0
