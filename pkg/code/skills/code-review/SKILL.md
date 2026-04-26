---
name: code-review
description: Review code changes and provide feedback on correctness, style, security, and potential issues.
when-to-use: When the user wants feedback on their changes before committing or submitting a PR.
arguments: [ref]
---
# Code Review

Review the current code changes and provide constructive feedback.

## Phase 1: Identify Changes

1. Run `git diff` to see unstaged changes, and `git diff --cached` for staged changes
2. If a specific ref was provided: `git diff ${ref}`
3. Run `git log --oneline -5` to understand the repo's conventions

## Phase 2: Launch Review Agents in Parallel

Use the `agent` tool to launch all agents concurrently in a single message. Pass each agent the full diff and the list of changed files.

### Agent 1: Correctness Review

For each changed file, check:

1. **Logic errors**: incorrect conditions, off-by-one errors, wrong operator, inverted boolean logic
2. **Edge cases**: nil/null/empty inputs, boundary values, zero-length collections, concurrent access
3. **Error handling**: errors swallowed or ignored at system boundaries, missing cleanup on error paths
4. **Resource leaks**: unclosed files, connections, channels, or goroutines that outlive their scope
5. **Race conditions**: shared mutable state accessed without synchronization in concurrent code
6. **API contract violations**: callers passing wrong types, missing required fields, ignoring return values

### Agent 2: Style & Consistency Review

For each changed file, check:

1. **Codebase consistency**: does the new code follow the patterns and conventions already established in surrounding code?
2. **Naming clarity**: are new names descriptive, unambiguous, and consistent with existing naming?
3. **Function complexity**: are new functions doing too much? Could they be decomposed?
4. **Abstraction level**: does the code operate at a consistent level of abstraction within each function?
5. **Dead code**: unused imports, unreachable branches, commented-out code

### Agent 3: Security & Testing Review

For each changed file, check:

**Security:**
1. Input validation at trust boundaries (user input, external APIs, file reads)
2. Injection vulnerabilities (SQL, command, XSS, template injection)
3. Sensitive data exposure (logging secrets, leaking PII, debug info in production)
4. Authentication/authorization gaps introduced by the change

**Testing:**
1. Are the changes adequately tested?
2. Are there edge cases that should have tests?
3. Do existing tests still cover the changed behavior, or do they need updating?

## Phase 3: Report

Wait for all agents to complete. Aggregate findings and present them organized by file, with specific line references. For each issue:
- **Severity**: error, warning, or suggestion
- **What**: the issue
- **Fix**: how to fix it

End with a brief overall assessment: is this change ready to merge, or does it need work?
