# Developer & AI Agent Guidelines

Welcome! This repository (**testrig**) is a Go library for orchestrating multi-service test environments. Since this is an open-source framework project with specific design philosophies, all contributing developers and AI agents must strictly adhere to the guidelines detailed below.

---

## 1. Collaboration & Design Decisions

* **User-Driven Design**: The human user makes **all** design decisions. Do not make unilateral design choices, API signature designs, or architectural assumptions.
* **Planning & Alignment**: Every change requires a planning phase with user involvement. Before modifying or adding code, propose an implementation plan and seek explicit user approval.
* **Human Review**: All changes must be reviewed and approved by a human developer before they are merged.
* **Unrelated Changes**: If you spot an unrelated issue or improvement while working on a task:
  * Do **not** bundle it into your current PR or branch.
  * Consult the user.
  * Recommend creating a new branch on top of `origin/master` or a merge train when relevant.

---

## 2. Branching & PR Strategy

* **Logical Changes**: Each branch must contain exactly one logical change. Keep changes minimal and focused.
* **PR to Master**: All features, bug fixes, and modifications must get to the `master` branch via a Pull Request (PR) and pass the automated CI pipeline.

---

## 3. Go Idiomatics & Defensive Design

* **Go Idiomatic**: Code must follow Go style guides, idioms, and standard formatting (`gofmt`, `goimports`).
* **Modular Architecture**: Keep modules separated and highly cohesive.
* **Defensive Encapsulation**: 
  * Design defensively when openness is not a feature.
  * Keep internal components, utility functions, and implementation details private/unexported or within `internal/` packages unless they are explicitly part of the public API.
* **Package Boundaries & Layout Invariants**:
  * All core public interface contracts (e.g., `api.Service`, `api.EnvHandle`, `api.LifecycleHook`, `api.Properties`) and test mock helpers (like `api.StubEnvHandle`) must reside in the `api/` package. The `api/` package must remain free of dependencies on the root package to prevent circular imports.
  * The root `testrig` package is responsible for the engine orchestration (like `Env`, `Stages`, etc.).
  * Pre-built services must implement `api.Service` and live in their own dedicated modules under the `/services/` directory.

---

## 4. Test-Driven Development (TDD) & Testing Standards

* **TDD Methodology**: Follow the Test-Driven Development (TDD) methodology. Write failing tests before writing the implementation to satisfy them.
* **Comprehensive Coverage**: Tests must cover all edge cases, input variations, error handling paths, and boundary conditions.
* **Stable & Reproducible Tests**: 
  * Avoid flaky tests and timing-sensitive assumptions.
  * **Never use sleep (`time.Sleep`)** to wait for asynchronous conditions in tests.
  * Always prefer **polling a condition** (e.g., helper loops with timeouts) or using synchronization primitives (channels, `sync.WaitGroup`, event listeners) to coordinate asynchronous test flows.
* **Fast Builds**: The build and test suite must run quickly. Prefer parallel testing (`t.Parallel()`) where relevant and possible to optimize local and CI execution times.

---

## 5. Monorepo Modules & Build Structure

The repository is structured as a Go monorepo with multiple independently versioned modules using Go Workspaces (`go.work`):

```
.                             github.com/sha1n/testrig            (engine; stdlib + golang.org/x/sync only)
├── dockerlog/                github.com/sha1n/testrig/dockerlog  (container log streaming helper)
├── services/oidc/            github.com/sha1n/testrig/services/oidc (OAuth/OIDC issuer service)
├── services/postgres/        github.com/sha1n/testrig/services/postgres (PostgreSQL testcontainer service)
├── services/wiremock/        github.com/sha1n/testrig/services/wiremock (WireMock testcontainer service)
├── examples/                 github.com/sha1n/testrig/examples   (internal demo apps; not published)
├── tools/                    github.com/sha1n/testrig/tools      (pinned developer tools; not published)
└── go.work                   Ties all modules together for local development
```

### Workspace Commands

Use the provided `Makefile` targets to build, lint, and test across the workspace:

* `make check`: Runs formatting, linting, and tests across all modules (default target).
* `make test`: Runs tests across all modules.
* `make lint`: Performs `go vet` and runs `golangci-lint` (using the version pinned in `tools/go.mod`).
* `make build-examples`: Compiles example application binaries into `bin/`.
* `make go-get`: Synchronizes workspace dependencies.
* `make tidy`: Runs `go mod tidy` across all modules.

---

## 6. Architecture Decisions & Change Tracking

* **Architecture Decision Records (ADRs)**: Every major architectural change, API signature modification, package layout restructuring, or design decision must be documented as a Decision Record in the `docs/decisions/` directory.
* **File Naming**: Save records using the format `docs/decisions/YYYY-MM-DD-<short-topic-name>.md`.
* **Content Template**: Each ADR must include:
  * **Title**: Descriptive title of the decision.
  * **Status**: `Proposed` | `Accepted` | `Superseded`. If a later decision overrides this one, update the old file's status to `Superseded` and link to the new file.
  * **Context**: The background, problem, or constraints driving the change.
  * **Decision**: The chosen implementation, design, or layout.
  * **Consequences**: The impact on the codebase, external clients, or downstream development (both positive and negative).
* **AI Agent Responsibility**: When implementing tasks, agents must check if their work introduces a design decision. If so, they must propose and write the corresponding ADR as part of their implementation plan.

