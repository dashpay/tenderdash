# Go Coding Style Guide

This guide establishes shared stylistic conventions for the Tenderdash codebase.
We expect all contributors to be familiar with [Effective Go](https://golang.org/doc/effective_go.html).
We also use [Uber's style guide](https://github.com/uber-go/guide/blob/master/style.md) as a starting point.

## Code Structure

Try to write in a logical order of importance so that someone scrolling down can
understand the functionality. A loose example:

* Constants, global and package-level variables
* Main Struct
* Options (only if critical to the struct; otherwise place in another file)
* Initialization / Start and stop of the service
* Msgs/Events
* Public functions (in order of importance)
* Private/helper functions
* Auxiliary structs and functions (can also be in a separate file)

## General

* Don't Repeat Yourself. Search the codebase before writing new code.
* Less code is better. Review code after implementation to ensure it cannot be written in a shorter form.
* Use `gofmt` (or `goimports`) to format all code upon saving.
* Use a linter (see below) and keep it happy where it makes sense.
* Leave godoc comments when they will help new developers.
* `TODO` should not be used. If important enough, file an issue.
* `BUG` / `FIXME` should be used sparingly.
* `XXX` may appear in WIP branches but must be removed before merging.
* Applications (e.g. CLIs/servers) *should* panic on unexpected unrecoverable
  errors and print a stack trace.

## Comments

* Use a space after the comment delimiter (e.g. `// your comment`).
* Non-sentence comments should begin with a lower case letter and end without a period.
* Sentence comments should be sentence-cased and end with a period.

## Linters

* Lint your changes to the code base, not the whole project
* [golangci-lint](https://github.com/golangci/golangci-lint) — see `.golangci.yml` for configuration.

## Naming and Conventions

* Reserve "Save" and "Load" for long-running persistence operations. For parsing bytes, use "Encode" or "Decode".
* Functions that return functions should have the suffix `Fn`.
* Names should not [stutter](https://blog.golang.org/package-names). For example, avoid:

  ```go
  type middleware struct {
  	middleware Middleware
  }
  ```

* Product names are capitalized ("Tenderdash", "Protobuf") except in command lines: `tenderdash --help`.
* Acronyms are all-caps: "RPC", "gRPC", "API", "MyID" (not "MyId").
* Prefer `errors.New()` over `fmt.Errorf()` unless you need format arguments.

## Importing Libraries

* Use [goimports](https://pkg.go.dev/golang.org/x/tools/cmd/goimports).
* Separate imports into three blocks: standard library, external, and application.
* Never use dot imports (`.`).
* Use the `_` import for side-effect-only imports.

## Testing

* All code must have tests.
* Use table-driven tests where possible and not cumbersome.
* Use [assert](https://pkg.go.dev/github.com/stretchr/testify/assert) and [require](https://pkg.go.dev/github.com/stretchr/testify/require) from testify.
* For mocks, use Testify [mock](https://pkg.go.dev/github.com/stretchr/testify/mock) with [Mockery](https://github.com/vektra/mockery) for autogeneration.

## Errors

* Ensure errors are concise, clear, and traceable.
* Use the stdlib `errors` package.
* Wrap errors with `fmt.Errorf()` and `%w`.
* Panic only when an internal invariant is broken. All other cases should return errors.

## Non-Go Code

All non-Go code (`*.proto`, `Makefile`, `*.sh`) should be formatted according
to the [EditorConfig](http://editorconfig.org/) in the repo root (`.editorconfig`).
