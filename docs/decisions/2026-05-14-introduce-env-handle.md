# Introduce EnvHandle on Service Start

* **Date**: 2026-05-14
* **Status**: Accepted

## Context

`Service.Start` took `*slog.Logger` and had no access to the environment's state or properties published by services in earlier stages of the same track. This prevented late-stage services from dynamically reading properties (like database credentials or hosts) published by earlier services.

## Decision

Replace the `*slog.Logger` parameter on `Service.Start` with an `EnvHandle` interface that exposes `Name()`, `Logger()`, and `Properties()`. Provide `StubEnvHandle` helper for standalone testing.

## Consequences

Services can read properties from earlier stages of their track dynamically, making configuration plumbing much cleaner.
