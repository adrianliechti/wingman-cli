---
name: run-tests
description: Run the project test suite, optionally scoped to one package
when-to-use: After code changes, or when the user asks to run or fix tests
arguments:
  - package
---

# Run Tests

Run the test suite and report failures with enough context to fix them.

1. If `${package}` is given, run `go test -cover ${package}`; otherwise run
   `go test -cover ./...`.
2. On failures, show the failing test names and the relevant assertion output,
   then look at the involved source and suggest a fix.
3. On success, report the package count and any coverage outliers.
