# Changelog

## Unreleased

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
