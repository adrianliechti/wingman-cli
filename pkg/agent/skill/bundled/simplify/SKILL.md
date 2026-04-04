---
name: simplify
description: Review changed code for reuse, quality, and efficiency, then fix any issues found.
when-to-use: After completing code changes, to clean up and improve code quality before committing.
---
# Simplify: Code Review and Cleanup

Review all changed files for reuse, quality, and efficiency. Fix any issues found.

## Phase 1: Identify Changes

Run `git diff` (or `git diff HEAD` if there are staged changes) to see what changed. If there are no git changes, review the most recently modified files that the user mentioned or that you edited earlier in this conversation.

## Phase 2: Review

Review each changed file for:

### Code Reuse
- Search for existing utilities and helpers that could replace newly written code
- Flag any new function that duplicates existing functionality
- Flag inline logic that could use an existing utility

### Code Quality
- Redundant state that duplicates existing state
- Parameter sprawl — adding new parameters instead of restructuring
- Copy-paste with slight variation that should be unified
- Leaky abstractions exposing internal details
- Unnecessary comments explaining WHAT instead of WHY

### Efficiency
- Unnecessary work: redundant computations, repeated file reads, duplicate API calls
- Missed concurrency: independent operations run sequentially
- Unnecessary existence checks before operations (TOCTOU)
- Unbounded data structures, missing cleanup

## Phase 3: Fix Issues

Fix each issue directly. If a finding is a false positive, skip it.

When done, briefly summarize what was fixed (or confirm the code was already clean).
