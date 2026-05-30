# Split Modules

* **Date**: 2026-05-08
* **Status**: Accepted

## Context

Storing the core orchestrator and all pre-built services (Postgres, Wiremock, OIDC) in a single module forced consumers to pull in all external dependencies (such as `testcontainers-go` and client libraries) even if they only needed one service.

## Decision

Split the repository into a Go workspace (`go.work`) with independently versioned modules for each pre-built service under the root directory, plus a `tools` module for development pinning.

## Consequences

Minimal dependency graphs for external consumers. While Go Workspaces (`go.work`) coordinate local workspace development, local `replace` directives are still maintained in sub-module `go.mod` files to allow direct builds/tests of sub-modules before their engine tags are published.
