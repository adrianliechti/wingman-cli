---
name: review
description: Review code changes and provide feedback on correctness, style, and potential issues.
when-to-use: When the user wants feedback on their changes before committing or submitting a PR.
arguments: [ref]
---
# Code Review

Review the current code changes and provide constructive feedback.

## Steps

1. Run `git diff` to see unstaged changes, and `git diff --cached` for staged changes
2. If a specific ref was provided: `git diff ${ref}`
3. For each changed file, analyze:

### Correctness
- Logic errors or edge cases not handled
- Missing error handling at system boundaries
- Race conditions in concurrent code
- Resource leaks (unclosed files, connections)

### Style & Conventions
- Consistency with the existing codebase style
- Naming clarity
- Function/method length and complexity

### Security
- Input validation at boundaries
- Injection vulnerabilities (SQL, command, XSS)
- Sensitive data exposure

### Testing
- Are the changes adequately tested?
- Are there edge cases that should have tests?

## Output

Provide feedback organized by file, with specific line references. For each issue:
- Severity: error, warning, or suggestion
- What the issue is
- How to fix it

End with a brief overall assessment.
