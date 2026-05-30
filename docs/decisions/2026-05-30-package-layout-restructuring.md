# Monorepo Package Layout Restructuring

* **Date**: 2026-05-30
* **Status**: Accepted

## Context

Defining core public interfaces in the root package and having services as sibling directories led to packaging limits. Separating public API surface from the root engine coordinator was necessary to prevent circular dependencies while moving services into a cleaner monorepo structure.

## Decision

Introduce the `api` sub-package under the root module containing core public interfaces and stubs (`Service`, `EnvHandle`, `LifecycleHook`, `Properties`, `StubEnvHandle`). Move services to `services/` directory and `pkg/dockerlog` to `dockerlog`.

## Consequences

Complete isolation of external extension contracts, zero-dependency `api` package avoiding import cycles, and structured module directory layout.
