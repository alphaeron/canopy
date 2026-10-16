# Project Overview
Name: Kit
Description: A modular code review tool
Go Version: follow go.mod
Architecture: Prefer simple, modular, idiomatic Go code with clear package boundaries and explicit dependencies.
Primary Goal: Produce correct, secure, readable, testable, and maintainable software while minimizing unnecessary complexity.

Before making changes, inspect the existing repository, read go.mod, review relevant packages and tests, and identify the established conventions. Follow the existing architecture unless there is a clear reason to change it.

Do not assume that every directory described in this document must exist. Adapt the guidance to the actual project.

# Core Commands

Run commands from the repository root unless otherwise specified.

* Build: `go build -v ./...`
* Test: `go test -v ./...`
* Run Tests with Coverage: `go test -cover ./...`
* Race Detection: `go test -race ./...`
* Lint: `golangci-lint run`
* Format: `gofmt -w . or go fmt ./...`
* Static Analysis: `go vet ./...`
* Dependency Verification: `go mod verify`
* Generate: `go generate ./... when the project uses code generation.`

# Command Guidelines

* Inspect the repository and available tooling before running commands that could modify files.
* Use the Go version specified in go.mod and the versions configured by CI.
* Run targeted tests while developing, then run the complete relevant test suite.
* Before finalizing changes, run go test ./... and golangci-lint run when the required tools and environment are available.
* Run go test -race ./... when changes involve concurrency, shared state, synchronization, or other race-sensitive behavior.
* Run go vet ./... when validating significant changes or when appropriate to the project.
* Do not claim a command succeeded unless it was actually executed successfully.
* If a required command cannot run because of missing tools, unavailable services, or environment limitations, report the limitation explicitly.
* Do not run code generators unless their use is established and the resulting changes are understood.
* Review generated changes and ensure that generated files are reproducible and consistent with the repository.

# Project Structure

Follow the existing repository layout. Use directories only when they provide a meaningful organizational or architectural benefit.

Common Go project directories include:
* cmd/: Application entry points. Each subdirectory generally represents a separate executable.
* internal/: Packages intended for use only within the enclosing module or permitted parent directory.
* pkg/: Packages explicitly intended to be reusable by external consumers, when appropriate.
* api/: API definitions, schemas, or specifications.
* proto/: Protocol Buffers and gRPC definitions, if applicable.
* configs/: Example or default configuration files, when needed.
* scripts/: Development, CI, deployment, or maintenance scripts.
* migrations/: Database migration files, if applicable.
* test/ or tests/: Integration-test support, fixtures, or external test assets, if needed.
* docs/: Project documentation.
* web/: Web application assets, if applicable.
* go.mod and go.sum: Module metadata and dependency checksums.
* Makefile: Optional, documented shortcuts for common development tasks.

## Project Layout Rules

* Do not create every conventional directory by default.
* Prefer a simple structure for small applications and libraries.
* Keep executable wiring and application startup in cmd/ when the project benefits from separating entry points from application logic.
* Put implementation details that should not be imported externally in internal/.
* Use pkg/ only when there is a genuine need for externally reusable packages; it is not mandatory for every Go project.
* Avoid generic catch-all packages such as common, helpers, misc, or utils when functionality can be placed in a cohesive domain package.
* Organize packages around cohesive responsibilities and domain concepts rather than arbitrary technical layers alone.
* Keep package dependencies explicit and avoid circular dependencies.
* Avoid creating abstractions or directory hierarchies solely to conform to a template.
* Do not move files or reorganize package boundaries without understanding the impact on imports, tests, builds, and consumers.
* Preserve public package APIs unless a change has been explicitly authorized.

Reference: https://github.com/golang-standards/project-layout

# Go Code Style & Conventions

## Formatting

* Follow standard Go formatting using gofmt.
* Use idiomatic Go naming, declarations, control flow, and error handling.
* Prefer conventional Go patterns over patterns borrowed unnecessarily from other languages.
* Do not introduce custom formatting rules that conflict with Go tooling.
* Use goimports if it is already part of the project's tooling or conventions.
* Keep formatting changes focused; avoid unrelated repository-wide formatting churn.

## Naming

* Use MixedCaps or mixedCaps, not snake_case, for Go identifiers.
* Use short receiver names, such as func (c *Client) Do().
* Choose names that communicate purpose and domain meaning.
* Prefer descriptive names for variables with broad scopes or complex responsibilities.
* Short names are appropriate for small scopes when their meaning is obvious, such as i for an index or r for a reader.
* Avoid unexplained abbreviations and redundant names.
* Avoid repeating package names in identifiers. For example, prefer http.Client over http.HTTPClient and user.User over user.UserModel, unless the additional context is necessary.
* Use initialisms consistently, such as ID, URL, HTTP, and JSON.
* Name functions according to their responsibility and level of abstraction. A high-level function may have a broad name such as Parse, while a specialized helper should describe its specific operation.
* Keep naming consistent with existing exported APIs and package conventions.

## Comments and Documentation

* Document exported declarations with comments that begin with the declaration's name.
* Explain intent, constraints, assumptions, invariants, and non-obvious decisions.
* Prefer comments that explain why code exists rather than restating what the code visibly does.
* Do not add comments that merely narrate straightforward loops, conditionals, assignments, or function calls.
* Refactor unclear code instead of relying on extensive comments to explain unnecessarily complicated implementation details.
* Update comments whenever the behavior or contract of the associated code changes.
* Document important concurrency guarantees, ownership rules, side effects, and error conditions.
* Keep comments concise, accurate, and consistent with the implementation.
* Do not leave obsolete comments, misleading examples, or unfinished TODOs without context.

## Functions and Complexity

* Keep functions focused on a single coherent responsibility.
* Prefer small, readable functions, but do not extract helpers solely to satisfy an arbitrary line count.
* Use early returns and guard clauses to reduce unnecessary nesting.
* Avoid deeply nested conditionals and complicated control flow.
* Extract helpers when doing so gives a meaningful name to a distinct operation, reduces complexity, or improves testability.
* Avoid both excessive duplication and excessive abstraction. Extract shared behavior when the duplication is meaningful and the resulting abstraction is cohesive.
* Keep function signatures understandable.
* When several parameters are related configuration options, consider a purpose-built options struct.
* Do not introduce an options struct for every function with multiple arguments; simple functions with a few obvious parameters are often clearer as they are.
* Avoid boolean parameters when their meaning is unclear at the call site. Prefer descriptive options or distinct functions when that improves readability.
* Keep side effects visible and limited to appropriate boundaries.
* Prefer returning results over modifying broad shared state.
* Avoid mutable package-level variables unless they serve a clear, justified purpose.

## Variable Scope and Declarations

* Declare variables as close as practical to their first use.
* Minimize the scope and lifetime of mutable state.
* Prefer := for concise local declarations when appropriate.
* Avoid unnecessary predeclarations that enlarge scope or obscure where a value originates.
* Be aware of variable shadowing, especially with := inside nested blocks and conditional statements.
* Ensure that short declarations do not unintentionally create new variables instead of updating existing ones.
* Prefer direct returns and locally scoped intermediate values over complicated state mutation.
* Use constants for fixed primitive values when doing so improves clarity and avoids unexplained magic values.
* Avoid global mutable state, especially when it introduces hidden dependencies or concurrency hazards.

## Error Handling

* Never ignore an error returned by a function unless intentionally discarding it is safe, justified, and documented where necessary.
* Do not use _ to silently discard meaningful errors.
* Return errors to callers when the current layer cannot handle them appropriately.
* Handle errors at the layer that has sufficient context to recover, report them, or translate them into an appropriate response.
* Wrap errors with contextual information using fmt.Errorf("doing X: %w", err) when additional context is useful.
* Avoid repeatedly wrapping errors with redundant context at every layer.
* Use errors.Is and errors.As to inspect wrapped errors.
* Prefer sentinel errors or custom error types when callers need to make reliable programmatic decisions.
* Define package-level sentinel errors with descriptive names, such as ErrNotFound, when they represent stable, meaningful conditions.
* Use custom error types when structured details, additional behavior, or typed inspection are needed.
* Do not compare err.Error() strings to determine error identity.
* Avoid exposing internal implementation details, sensitive values, credentials, or private infrastructure information in externally visible errors.
* Preserve the original error when wrapping it so callers can inspect its cause.
* Return meaningful errors instead of panicking for ordinary operational failures.
* Do not replace a useful error with an unrelated generic error when the original can be preserved.
* When returning both a result and an error, define and maintain a consistent contract for the result on failure. Return the appropriate zero value when practical; do not invent exported mutable zero-value objects merely to avoid returning a zero value.
* Use errors to communicate failures rather than mixing error conditions into ordinary return values without a clear contract.

Example:

```go
func LoadConfig(path string) (Config, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return Config{}, fmt.Errorf("read config %q: %w", path, err)
    }

    var cfg Config
    if err := json.Unmarshal(data, &cfg); err != nil {
        return Config{}, fmt.Errorf("decode config %q: %w", path, err)
    }

    return cfg, nil
}
```

## Interfaces and Abstraction

* Define interfaces where they are consumed, rather than automatically alongside implementations.
* Keep interfaces small and focused on the behavior required by the consumer.
* Prefer concrete types when an interface does not provide a meaningful benefit.
* Accept interfaces when doing so improves substitution, testing, or decoupling.
* Return concrete types by default unless an interface return type is important to the package's abstraction or API contract.
* Avoid interfaces created solely for mocking without a real behavioral boundary.
* Avoid large interfaces that force implementations to provide irrelevant methods.
* Use compile-time interface assertions when they document and verify an important contract.

Example:

```go
type UserReader interface {
    GetUser(ctx context.Context, id string) (User, error)
}
```

```go
type UserService struct {
    users UserReader
}

var _ UserReader = (*SQLUserRepository)(nil)
```

* Prefer explicit, type-safe APIs over unnecessary use of any or interface{}.
* Remember that any is an alias for interface{} in modern Go; it does not provide additional type safety.
* Use generics when they simplify genuinely reusable, type-safe algorithms or data structures.
* Do not recreate generic behavior using repeated type assertions when Go's type system can express the requirement directly.
* When interacting with a third-party API that returns any, contain type assertions and validation within a small, well-defined boundary.
* Check type assertions using the comma-ok idiom when failure is possible.
* Avoid using embedded interfaces merely to satisfy a compile-time interface contract. An embedded nil interface can lead to runtime panics when its methods are invoked.
* Prefer explicit method implementations or delegation when behavior must be reliable and clear.
* Avoid premature abstraction. Introduce interfaces and shared abstractions when there is a concrete need.

## Pointers, Values, and Ownership

* Use pointers when mutation, identity, large struct copying, or nilability is part of the intended contract.
* Prefer values for small data structures when copying is safe and straightforward.
* Do not assume that pointers are inherently better or worse than values; choose according to semantics and performance requirements.
* Avoid unintentionally sharing mutable objects between callers and internal state.
* Be careful when storing pointers supplied by callers or returning internal pointers from repositories, caches, and stores.
* Copy values or provide explicit ownership rules when external mutation could corrupt internal state.
* Remember that copying a struct containing maps, slices, pointers, channels, or interfaces does not necessarily create an independent deep copy.
* Do not expose mutable internal state without a clear reason.
* Initialize required maps, channels, and dependencies through constructors or explicit initialization paths.
* Design zero values to be useful when practical, but do not add complex lazy initialization merely to eliminate every possible nil value.
* Handle nil values explicitly where nil is valid or likely to occur.
* Avoid unnecessary pointer-to-pointer patterns and redundant allocations.
* Do not return mutable global zero-value objects as error substitutes.
* Consider concurrency safety whenever mutable data is shared across goroutines.

## Concurrency and Context

* Use context.Context as the first parameter for operations that need cancellation, deadlines, or request-scoped values.
* Do not store request contexts in structs for later use.
* Propagate context through network requests, database calls, and other cancellable operations.
* Respect cancellation and deadlines wherever supported.
* Avoid unbounded goroutine creation and goroutine leaks.
* Ensure goroutines have clear termination conditions and ownership.
* Use channels, mutexes, atomics, or other synchronization mechanisms according to the problem; do not default to one mechanism without considering the design.
* Avoid unbuffered channels or buffered channels by habit alone. Choose channel capacity based on the communication and lifecycle requirements.
* Do not close channels from receivers or from multiple owners. Establish clear ownership of channel closure.
* Do not use sleeps as synchronization in tests or production code when explicit synchronization is available.
* Protect shared mutable state and avoid data races.
* Prefer structured lifecycle management: make it clear who starts work, who cancels it, and who waits for it to finish.
* Use sync.WaitGroup, errgroup, or equivalent mechanisms when appropriate to coordinate concurrent tasks.
* Check errors returned by concurrent operations and propagate failures meaningfully.
* Run race-enabled tests for concurrency-sensitive changes.

## Nil Values and Zero Values

* Understand the zero-value behavior of structs, pointers, maps, slices, interfaces, and channels.
* Do not assume that a zero-value struct has all of its internal dependencies initialized.
* Ensure constructors establish required invariants when a type cannot safely operate with its zero value.
* Prefer APIs whose valid states are easy to understand and difficult to misuse.
* Return concrete zero values on failure when appropriate, together with a meaningful error.
* Do not introduce defensive nil checks indiscriminately; validate nil when it is possible under the API contract or necessary to prevent invalid behavior.
* Avoid exposing partially initialized objects.
* Make nil behavior explicit for public APIs when it matters.

## Closures and Callbacks

* Use functions as parameters when they provide a simple, clear way to customize behavior.
* Prefer closures for small, cohesive operations or callbacks.
* Use an interface when behavior has multiple related methods, substantial state, or a more complex lifecycle.
* Avoid unnecessarily complicated function signatures and deeply nested callbacks.
* Keep captured mutable state limited and understandable.
* Be aware that closures can outlive the scope in which they were created and may retain references to mutable objects.
* Avoid introducing callback abstractions when a direct function call is clearer.

## Testing

* Write unit tests for new behavior, exported functions, and bug fixes.
* Add regression tests for bugs whenever practical.
* Prefer table-driven tests for related cases with multiple inputs and expected outcomes.
* Use descriptive test names and keep tests focused on observable behavior.
* Include success cases, failure cases, boundary conditions, and invalid inputs where relevant.
* Test error identity with errors.Is and error types with errors.As when those are part of the contract.
* Test cancellation, timeouts, and concurrent behavior where applicable.
* Use the standard testing package unless the project already uses an appropriate testing framework.
* Prefer small, purpose-built test doubles over complex mocking frameworks.
* Use interfaces at genuine dependency boundaries to make components testable.
* Avoid tests that depend on execution order, external network availability, real credentials, or shared mutable state unless explicitly designed as integration tests.
* Do not make tests pass by weakening assertions or removing meaningful test coverage.
* Avoid arbitrary sleeps and flaky timing assumptions.
* Use `t.Parallel()` only when the test is safe to execute concurrently.
* Use `t.Helper()` in reusable test helpers.
* Use `t.Cleanup()` for cleanup that must run after a test.
* Use `t.TempDir()` for temporary filesystem fixtures.
* Use httptest for HTTP handlers and servers where appropriate.
* Keep unit tests deterministic and fast.
* Separate integration tests from unit tests when they have materially different infrastructure requirements.
* Add benchmarks when performance is a meaningful concern; do not optimize based solely on assumptions.
* Run `go test -race ./...` when changes could introduce races.

## Security and Configuration

* Never commit hardcoded secrets, API keys, tokens, passwords, or real credentials.
* Never use production credentials in tests.
* Read configuration from established configuration mechanisms, such as environment variables, configuration files, or explicitly injected settings.
* Validate configuration at application startup or at the boundary where it enters the system.
* Avoid silently accepting invalid configuration values.
* Do not log passwords, authentication tokens, session identifiers, or sensitive personal information.
* Validate and constrain untrusted input at system boundaries.
* Use parameterized database queries instead of constructing SQL from untrusted strings.
* Avoid exposing internal errors or stack details to external clients.
* Follow least-privilege principles for credentials, file permissions, and external service access.
* Keep TLS verification enabled unless there is a specifically authorized, secure alternative.
* Avoid introducing new dependencies for trivial functionality that the standard library already provides adequately.

## Dependencies and Modules

* Follow the existing module structure and dependency management conventions.
* Ask before adding a new third-party Go dependency.
* Before proposing a dependency, consider whether the standard library or existing project dependencies provide a suitable solution.
* Prefer actively maintained, well-scoped dependencies with appropriate licensing and a reasonable security posture.
* Do not add a dependency solely to avoid a small amount of clear, maintainable code.
* Do not manually edit go.sum to bypass dependency verification.
* Keep go.mod and go.sum consistent.
* Do not run broad dependency upgrades unless explicitly requested or necessary for the task.
* Review dependency changes for transitive dependencies and compatibility.
* Do not vendor dependencies unless the repository already uses vendoring or the change is authorized.
* Preserve the project's supported Go version and avoid using language or standard-library features newer than that version.
* Use go mod tidy only when needed and review all resulting changes.

## API and Database Changes

* Ask before modifying public APIs, exported types, configuration structs, or externally relied-upon behavior.
* Ask before modifying database schemas, migration history, or data-retention behavior.
* Preserve backward compatibility where practical.
* Prefer additive API changes over breaking changes when possible.
* Validate inputs and return actionable errors at API boundaries.
* Keep transport concerns, such as HTTP status codes and request serialization, separate from domain logic when appropriate.
* Use transactions when a database operation requires atomicity.
* Propagate context to database operations.
* Avoid holding database transactions or locks longer than necessary.
* Treat changes to serialization formats, database constraints, and configuration defaults as potentially breaking changes.
* Update API documentation, migration scripts, tests, and examples when an authorized change requires them.

## Application Architecture

* Keep application startup and dependency wiring separate from core business logic when the project structure supports that separation.
* Make dependencies explicit through constructor parameters or other clear initialization mechanisms.
* Avoid hidden dependencies on global state, environment variables, or singleton objects deep inside business logic.
* Keep domain logic independent of transport and infrastructure details when that separation provides practical value.
* Keep persistence, networking, filesystem access, and other side effects at clearly defined boundaries.
* Avoid circular dependencies and unnecessary cross-package coupling.
* Do not introduce layers that merely forward calls without providing meaningful behavior or separation.
* Prefer composition and small, focused types over unnecessary inheritance-like structures.
* Keep package responsibilities cohesive.
* Use dependency injection proportionately; do not build a framework when a constructor and a few interfaces suffice.
* Avoid changing architectural patterns across unrelated parts of the project without a clear need.

## Logging and Observability

* Use the project's existing logging library and conventions.
* Include relevant context in logs without exposing sensitive information.
* Avoid logging the same error repeatedly at every layer.
* Log errors where the application can meaningfully handle, report, or observe them.
* Use structured logging when supported by the project.
* Keep user-facing error messages appropriate for the audience.
* Avoid using logs as a substitute for returning errors or exposing required metrics.
* Do not add logging to every function by default.

## Performance and Resource Management

* Prioritize correctness, clarity, and maintainability before speculative optimization.
* Measure performance before introducing complicated optimizations.
* Release resources promptly and reliably.
* Use defer for cleanup when its scope and timing are appropriate.
* Close response bodies, files, and other resources according to their API contracts.
* Avoid unnecessary allocations and copies in performance-sensitive code when measurements justify doing so.
* Consider the ownership and lifetime of slices, maps, pointers, and buffers.
* Avoid unbounded caches, queues, retries, or background work.
* Set sensible timeouts and cancellation behavior for external operations.
* Avoid introducing global caches or pools without a clear lifecycle and demonstrated need.

## Documentation and Generated Files

* Update the README when user-facing behavior, setup instructions, or common commands change.
* Document configuration options, required environment variables, and operational prerequisites.
* Keep examples aligned with actual APIs and supported Go versions.
* Update API specifications and package documentation when relevant.
* Treat generated files as outputs of their generators.
* Do not manually modify generated files unless the repository explicitly requires it.
* Update the source definitions and generator inputs when changing generated output.
* Preserve licensing and attribution notices.

# Boundaries (Tiered Rules)

## Always Do

* Inspect relevant source files, tests, go.mod, and repository conventions before making changes.
* Keep changes focused on the requested task.
* Follow idiomatic Go style and format Go code with gofmt.
* Run targeted tests during development.
* Run `go test ./...` and `golangci-lint run` before finalizing changes when the tools and environment are available.
* Write tests for new behavior and bug fixes.
* Use context.Context for operations that require cancellation, deadlines, or request-scoped values.
* Propagate and wrap errors appropriately.
* Keep functions cohesive, state localized, and dependencies explicit.
* Preserve existing APIs and behavior unless a change is authorized.
* Review the final diff for unintended changes, generated artifacts, secrets, and unrelated formatting.
* Report the commands executed, their outcomes, and any validation that remains incomplete.

## Ask First

* Before adding a new third-party Go dependency.
* Before modifying database schemas, migration history, or production data.
* Before modifying public APIs, exported types, or externally consumed configuration structures.
* Before introducing breaking behavior or changing compatibility guarantees.
* Before refactoring core package boundaries or moving files across packages.
* Before changing the overall architecture or introducing a new architectural framework.
* Before changing authentication, authorization, cryptographic behavior, or security-sensitive defaults.
* Before changing deployment infrastructure, CI/CD workflows, or release procedures in ways that materially affect operations.
* Before removing established functionality, tests, documentation, or generated artifacts whose purpose is unclear.

## Never Do

* Never use panic() for normal operational error handling.
* Never commit hardcoded secrets, API keys, tokens, passwords, or real credentials.
* Never silently ignore meaningful errors.
* Never compare error strings to identify specific error conditions.
* Never use global mutable state without a clear justification.
* Never expose internal mutable state unintentionally.
* Never introduce goroutine leaks, unbounded background work, or unsynchronized shared-state access.
* Never disable linters with //nolint without a brief explanation of why the exception is necessary.
* Never use //nolint to conceal an unresolved correctness, security, or maintainability problem.
* Never add unnecessary interfaces, wrappers, abstractions, or directories solely to satisfy a style preference.
* Never replace type-safe APIs with any merely for convenience when a concrete type or generic abstraction is more appropriate.
* Never alter tests to hide a regression or weaken meaningful assertions.
* Never claim tests, builds, linting, or other checks passed unless they were actually run and passed.
* Never make unrelated changes that increase review scope without a clear reason.

# Definition of Done

A change is ready for review when:
* The implementation satisfies the requested behavior.
* Relevant tests have been added or updated.
* Go code is formatted.
* Errors and edge cases are handled appropriately.
* Concurrency and resource lifecycles are correct where applicable.
* No unintended public API or configuration changes have been introduced.
* Documentation has been updated when needed.
* Relevant tests and lint checks have passed, or any inability to run them is clearly reported.
* The final diff contains no unrelated changes, accidental secrets, or unexplained generated artifacts.
* The final response summarizes the changes and reports validation results honestly.

# References

* Go Project Layout: https://github.com/golang-standards/project-layout
* Clean Go Code: https://github.com/Pungyeon/clean-go-article
* Effective Go: https://go.dev/doc/effective_go
* Go Code Review Comments: https://go.dev/wiki/CodeReviewComments
* Go Documentation: https://go.dev/doc/
* Go Blog: https://go.dev/blog/
