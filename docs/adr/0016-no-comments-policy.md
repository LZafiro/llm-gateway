# 0016. No comments in code

- Status: Accepted
- Date: 2026-10-01

## Context

The codebase should read cleanly on its own. Comments drift from the code, and the reasoning behind decisions already has a home.

## Decision

Go source files contain no comments. Names, small functions and tests explain *what*. ADRs and specs explain *why*.

Allowed exceptions:

- compiler and tool directives: `//go:embed`, `//go:build`, `//go:generate`
- `//nolint:<linter>` when unavoidable
- generated files with the `Code generated ... DO NOT EDIT.` header

A CI step fails the build when a non-generated `.go` file contains `//` or `/*` comments outside these exceptions. `golangci-lint` rules that require doc comments on exported identifiers are disabled. Formatting is enforced with `gofumpt` and `goimports`.

## Consequences

- The policy is a verifiable property of the repository, not a convention.
- Non-obvious code must be made obvious through naming or extraction, or justified in an ADR.
