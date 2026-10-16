# Kit — Agent Instructions

## Project

- **Name:** Kit
- **Purpose:** Modular code review tool
- **Language:** Go; follow the version in `go.mod`
- **Design:** Simple, idiomatic, modular, secure, testable Go code.

Follow existing repository conventions and architecture. Inspect relevant source files, tests, and configuration before changing code. Do not assume documented directories or tools exist.

## Execution Efficiency

- Minimize context usage, tool calls, shell commands, and unnecessary file reads.
- Inspect only files relevant to the task; expand the scope when dependencies or behavior require it.
- Prefer targeted searches and tests over repository-wide exploration.
- Do not reread files or repeat commands when their results are still valid.
- Do not run builds, tests, linters, generators, or dependency checks speculatively.
- Choose the smallest validation set that provides reasonable confidence in the change.
- Run broader checks for significant changes, cross-package changes, or when explicitly requested.
- Never run destructive commands or overwrite user changes without authorization.
- Do not modify unrelated files or reformat unrelated code.
- Report validation limitations honestly. Never claim a command passed unless it ran successfully.

## Go Conventions

- Follow `gofmt`, the Go version in `go.mod`, and existing project conventions.
- Use idiomatic naming, control flow, error handling, and package organization.
- Keep functions focused and cohesive. Extract helpers when they improve clarity, reuse, or testability.
- Prefer concrete types and small interfaces defined by their consumers. Avoid speculative abstractions, unnecessary wrappers, and premature generalization.
- Make dependencies explicit. Keep package boundaries cohesive and avoid circular dependencies.
- Use pointers when mutation, identity, nilability, or copy cost justifies them; otherwise prefer values when appropriate.
- Keep mutable state local. Avoid global mutable state unless clearly justified.
- Use `context.Context` as the first parameter for operations that need cancellation, deadlines, or request-scoped values. Propagate it through supported I/O operations.
- Handle nil values and zero values according to the API contract. Avoid unnecessary defensive checks.
- Use generics when they simplify genuinely reusable, type-safe code; prefer concrete types otherwise.
- Preserve existing public APIs and behavior unless the task authorizes a change.

## Errors and Comments

- Handle meaningful errors; never silently discard them.
- Wrap errors with useful context using `%w` when appropriate. Avoid redundant wrapping.
- Use `errors.Is` and `errors.As` for error inspection. Never identify errors by comparing error strings.
- Return errors for ordinary operational failures rather than panicking.
- Document exported declarations and non-obvious behavior, invariants, ownership, concurrency, and side effects.
- Prefer concise comments explaining why something is necessary over comments restating the code.
- Update outdated comments when behavior changes.

## Testing and Validation

Validation is task-dependent. Do not execute every available check by default.

1. Add or update focused tests for new behavior and bug fixes.
2. Run the narrowest relevant tests first.
3. Run `go test ./...` when changes are broad, affect shared behavior, or warrant full-suite confidence.
4. Run `golangci-lint run` when linting is relevant, requested, or required by project CI.
5. Run `go test -race` for concurrency-sensitive changes.
6. Run `go vet ./...` when appropriate for significant changes or suspected static-analysis issues.
7. Run build, coverage, and dependency verification commands only when they provide value for the task or are explicitly required.

Useful commands, from the repository root:

- Targeted tests: `go test ./path/to/package`
- Full test suite: `go test ./...`
- Build: `go build ./...`
- Lint: `golangci-lint run`
- Static analysis: `go vet ./...`
- Race detection: `go test -race ./...`
- Coverage: `go test -cover ./...`
- Dependency integrity: `go mod verify`

Use actual package paths discovered in the repository. Do not run a command simply because it appears in this list.

Keep tests deterministic and focused. Prefer table-driven tests for related cases. Use `t.Helper()`, `t.Cleanup()`, and `t.TempDir()` where appropriate. Avoid arbitrary sleeps, flaky timing assumptions, unnecessary external services, and tests that depend on execution order. Do not weaken tests to make a change pass.

## Security and Dependencies

- Never commit secrets, credentials, tokens, or production data.
- Validate untrusted input at system boundaries.
- Do not log credentials or sensitive information.
- Use parameterized database queries and preserve TLS verification.
- Follow least-privilege principles and established configuration mechanisms.
- Avoid unnecessary dependencies; prefer the standard library or existing dependencies when suitable.
- **Ask before adding a third-party dependency.**
- Preserve supported Go compatibility and keep `go.mod` and `go.sum` consistent.
- Do not perform broad dependency upgrades, vendor dependencies, or run `go mod tidy` without a concrete need.
- Review dependency changes and generated output before accepting them.

## Architecture and Compatibility

- Follow the existing repository layout; do not create conventional directories without a real need.
- Keep business logic separate from infrastructure where this provides practical value.
- Use explicit constructors and dependency injection proportionately; do not introduce frameworks unnecessarily.
- Keep side effects at appropriate boundaries and manage resource lifecycles correctly.
- Avoid goroutine leaks, unbounded background work, data races, and unsynchronized shared state.
- Propagate cancellation and errors through concurrent operations.
- Use the existing logging, configuration, persistence, and testing conventions.
- Treat public APIs, exported types, configuration defaults, serialization formats, and database schemas as compatibility-sensitive.

## Changes Requiring Prior Approval

Ask before:

- Adding third-party dependencies.
- Changing public APIs, exported types, or externally consumed configuration.
- Modifying database schemas, migration history, or production data.
- Introducing breaking behavior or changing compatibility guarantees.
- Refactoring major package boundaries or introducing a new architectural framework.
- Changing authentication, authorization, cryptographic behavior, or security-sensitive defaults.
- Changing deployment, release, or CI/CD behavior materially.
- Removing established functionality or tests whose purpose is unclear.

Do not ask for approval for ordinary, focused implementation details within the requested task.

## Formatting and Generated Files

- Format changed Go files with `gofmt`.
- Keep formatting changes focused.
- Follow existing code-generation conventions. Do not run generators unless required and established by the repository.
- Treat generated files as generator outputs; update their source definitions when applicable.
- Update documentation only when the change affects documented behavior, setup, configuration, or usage.

## Definition of Done

Before finishing:

- Verify the implementation meets the requested behavior.
- Add or update relevant tests.
- Review the final diff for correctness, unintended changes, secrets, and generated artifacts.
- Run proportionate validation and report actual outcomes.
- Clearly disclose checks that were skipped or could not run when relevant.
- Summarize the changes and any remaining risks concisely.

## References

Consult these only when relevant to the task:

- Effective Go: https://go.dev/doc/effective_go
- Go Code Review Comments: https://go.dev/wiki/CodeReviewComments
- Go Documentation: https://go.dev/doc/
- Go Project Layout: https://github.com/golang-standards/project-layout
