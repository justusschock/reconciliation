# go-reconciliation documentation

Requirements (PRDs) and architecture decisions (ADRs) for
`github.com/justusschock/go-reconciliation`. The process is described in
[ADR-0001](adr/0001-record-architecture-decisions.md) and
[AGENTS.md](../AGENTS.md). New documents start from the templates:
[PRD template](prd/TEMPLATE.md), [ADR template](adr/TEMPLATE.md).

## Product requirements

| PRD | Title | Status | Implementation |
|---|---|---|---|
| [0001](prd/0001-go-reconciliation.md) | go-reconciliation | Accepted | Complete |

## Architecture decisions

| ADR | Title | Status |
|---|---|---|
| [0001](adr/0001-record-architecture-decisions.md) | Record architecture decisions | Accepted |
| [0002](adr/0002-generic-adapter-driven-api.md) | Generic adapter-driven API shape | Accepted |
| [0003](adr/0003-dependency-policy.md) | Dependency policy | Accepted |
| [0004](adr/0004-error-semantics-and-retry-classification.md) | Error semantics and retry classification | Accepted |
| [0005](adr/0005-validated-action-dags-and-dependency-safe-rollback.md) | Validated action DAGs and dependency-safe rollback | Accepted |
| [0006](adr/0006-concurrency-cancellation-and-shutdown.md) | Concurrency, cancellation and shutdown model | Accepted |
| [0007](adr/0007-value-ownership.md) | Value ownership: shallow by default, explicit clones | Accepted |
| [0008](adr/0008-non-blocking-status-reporting.md) | Status reporting through context-carried, non-blocking sinks | Accepted |
| [0009](adr/0009-handoff-artifact-format.md) | Handoff artifact format | Accepted |
| [0010](adr/0010-public-api-boundary.md) | Public API boundary | Accepted |
| [0011](adr/0011-supported-go-versions.md) | Supported Go versions | Accepted |
| [0012](adr/0012-testing-strategy.md) | Testing strategy | Accepted |
| [0013](adr/0013-continuous-integration.md) | Continuous integration and local hooks | Accepted |
