# Fluent Builder API and Stages

* **Date**: 2026-05-07
* **Status**: Accepted

## Context

The original `Env` configuration relied on functional options (`Option` function arguments passed to `New`). This made complex multi-stage configurations awkward to read and write. Additionally, services in an environment started in parallel with no execution ordering support, which made ordering dependencies (e.g. running seed migrations only after Postgres started) impossible.

## Decision

Introduce a chainable fluent builder API on `*Env` (`With`, `WithLogger`, `WithLifecycleHooks`). Add `Stages` tracks (`NewStages().Then()`) to support sequential execution boundaries within concurrently running tracks. Delete old functional options.

## Consequences

Improved readability of test setup, clean isolation between parallel tracks, and predictable execution order for dependent services.
