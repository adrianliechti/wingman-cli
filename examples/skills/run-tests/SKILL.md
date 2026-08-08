---
name: run-tests
description: Run or fix the project test suite, optionally scoped to one package. Use after code changes or when the user asks to verify tests.
license: MIT
compatibility: Requires Go and a project with a go.mod file.
metadata:
  category: testing
  example: wingman
allowed-tools: Shell(go:*) Shell(sh:*) Read
arguments: [package]
argument-hint: "[package-or-./...]"
---

# Run Tests

Run the test suite for `$package` and report failures with enough context to fix
them. An empty package means `./...`.

1. Read `${SKILL_DIR}/references/reporting.md` before running anything.
2. Run `sh "${SKILL_DIR}/scripts/run.sh" "${PROJECT_DIR}" "$package"`.
   The helper defaults an empty package to `./...` and propagates the test exit
   status.
3. On failures, show the failing test names and the relevant assertion output,
   then look at the involved source and suggest a fix.
4. On success, report the package count and any coverage outliers.

The original invocation text is `$ARGUMENTS`; use it only when quoting the
user's requested scope back to them.
