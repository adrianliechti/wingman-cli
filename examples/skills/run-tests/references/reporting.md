# Test reporting contract

- Record the exact command and whether it passed.
- Preserve failing package and test names verbatim.
- Include only the assertion or panic output needed to understand a failure.
- Separate failures caused by the code from checks that could not run because a
  dependency or service was unavailable.
- Never claim that the full suite passed after running only a scoped package.
