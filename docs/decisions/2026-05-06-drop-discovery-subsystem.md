# Drop Discovery Subsystem

* **Date**: 2026-05-06
* **Status**: Accepted

## Context

The initial implementation had a cross-process discovery and reuse subsystem (`WithDiscovery`, `DiscoveryProvider`, `DiscoveryStore`) designed to share container fixtures across multiple environment instances. However, coordinating startup/shutdown ownership between different environment instances introduced critical race conditions and stop coordination bugs.

## Decision

Completely delete the discovery and cross-env reuse layers. The `Service` interface was simplified to 4 methods (removing `Identifier`).

## Consequences

Code complexity was drastically reduced, and resource lifecycle guarantees became absolute (each environment owns and terminates its own services).
