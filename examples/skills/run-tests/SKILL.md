---
name: run-tests
description: Run or fix the project test suite, optionally scoped to one package. Use after code changes or when the user asks to verify tests.
---

# Run Tests

Run the test suite and report failures with enough context to fix them.

1. If `${ARGUMENTS}` is given, run `go test -cover ${ARGUMENTS}`; otherwise run
   `go test -cover ./...`.
2. On failures, show the failing test names and the relevant assertion output,
   then look at the involved source and suggest a fix.
3. On success, report the package count and any coverage outliers.
