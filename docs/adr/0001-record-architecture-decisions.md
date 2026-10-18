# ADR 0001: Record architecture decisions

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-10-18 |
| Deciders | Justus Perillieux |
| Supersedes | — |
| Related | PRD-0001 |

## Context

Recorded retrospectively for the first public release. The library was built
test-first, with design reasoning kept in commit messages and doc comments.
Outside contributors need the reasoning in one discoverable place, and future
changes need a process that keeps requirements, decisions and code aligned.

## Decision drivers

- D1: Contributors must be able to find why the API looks the way it does.
- D2: Decisions must not be silently rewritten after the fact.
- D3: Behaviour changes should be agreed before they are implemented.

## Considered options

1. **MADR-style ADRs plus PRDs in `docs/`, docs-first**
   - Pro: lightweight Markdown, reviewed in the same pull requests as code.
   - Pro: explicit options and consequences make trade-offs reviewable.
   - Con: adds a document to every significant change.
2. **Design notes in doc comments and commit messages only**
   - Pro: no extra files.
   - Con: reasoning is scattered and hard to find; alternatives are lost.
3. **External wiki or issue tracker**
   - Pro: easy to edit.
   - Con: drifts from the code and is not versioned with it.

## Decision

We will record requirements as PRDs (`docs/prd/NNNN-title.md`) and decisions
as MADR-style ADRs (`docs/adr/NNNN-title.md`), both from the templates in
`docs/`. Documents come first: a behaviour change starts with a PRD or ADR
change that is reviewed as `Proposed` and becomes `Accepted` when merged.
Accepted ADRs are immutable except for their status line; a changed decision
gets a new ADR with `Supersedes: ADR-NNNN`, and the old one becomes
`Superseded by ADR-MMMM`. `docs/README.md` indexes every document and its
status. Tests cite the requirement IDs they verify.

## Consequences

### Positive
- The reasoning behind the API is reviewable and versioned with the code.
- Requirement IDs give traceability from PRD to test.

### Negative
- Significant changes carry documentation overhead.

### Follow-ups
- Keep `docs/README.md` current with every added or superseded document.

## Links

- [docs/README.md](../README.md)
- [AGENTS.md](../../AGENTS.md)
