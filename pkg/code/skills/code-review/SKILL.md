---
name: code-review
description: High-precision review of local changes, branch diffs, or pull requests for real bugs, silent failures, compatibility breaks, missing behavioral tests, security regressions, and explicit project-rule violations.
when-to-use: When the user wants trustworthy feedback before committing or merging, including targeted review of tests, error handling, types, public contracts, or security.
arguments: [ref]
---
# Code Review

Review the requested changes and report only findings independently verified as real. Optimize for a short list a senior engineer would act on, not exhaustive commentary. Do not edit files, post comments, add labels, or approve the change.

## Phase 1: Resolve the review scope

Use exactly one scope and state it:

- No `${ref}`: review staged and unstaged changes against `HEAD`, including relevant untracked files.
- Git ref: review the merge-base diff from `${ref}` to `HEAD`, plus worktree changes only if the user explicitly included them.
- Pull request number or URL: use the repository's PR tooling to obtain metadata and the diff. Do not review a closed, draft, generated, or already-reviewed PR unless the user explicitly asks.

Gather `git status --short`, the changed-file list, diff statistics, and the full diff. Read root and applicable nested `AGENTS.md`/`CLAUDE.md` files. Inspect nearby code, callers, tests, comments, and history only where they can establish a contract. If the diff is empty, say so and stop.

Summarize the change's intent in one or two sentences before reviewing. If intent cannot be inferred from the request, diff, or PR description, state the uncertainty instead of inventing it.

## Phase 2: Find candidates

Launch the applicable read-only review agents concurrently. Give each the scope, intent, changed-file list, full diff, and relevant guideline paths. Ask for candidate findings as `file:line`, claim, concrete failure scenario, and evidence. Skip a specialist when the diff has no relevant surface.

### Correctness and silent failures (`code-reviewer`)

Trace changed behavior through success and failure paths. Check boundaries, concurrency, cleanup, retries, partial writes, swallowed errors, misleading fallbacks, and errors that are logged but returned as success.

### Contracts, types, and compatibility (`code-reviewer`)

Check public APIs, CLI flags and output, configuration, protocols, persisted data, migrations, serialization, and resumability. For new or changed types, ask whether invalid states remain representable and whether invariants are enforced at the boundary. A compatibility finding must name a concrete affected caller, consumer, or stored value.

### Tests and behavioral coverage (`code-reviewer`)

Check whether changed behavior is exercised at the right layer. Flag a test gap only when you can name a meaningful regression and the exact case that would catch it. Reject assertion-light tests, tests of mocks rather than behavior, and tests that merely restate static definitions.

### Security (`security`)

Check changed trust boundaries, authorization, injection sinks, secrets or PII exposure, unsafe parsing, and cryptographic misuse. Require a concrete attacker-controlled data flow. This is a diff review, not a full audit; suggest `/security-review` when broader coverage is warranted.

### Project rules and local context (`code-reviewer` or `explore`)

Check applicable instruction files, nearby comments, and relevant history. Quote the exact rule or contract. Do not turn writing guidance into a review finding unless the diff demonstrably violates it.

Do not report formatting, naming preference, optional refactors, praise, or change size by itself. If a large diff mixes independent concerns or prevents reliable review, mention the smallest coherent split as a scope risk, not a bug.

## Phase 3: Verify -- confirm each candidate before it survives

Collapse duplicates by root cause. For each remaining candidate, launch a fresh skeptical verifier in parallel. The finder must never verify its own claim. Give the verifier the diff and claim, but not the finder's reasoning:

> Default assumption: this finding is WRONG. Re-read the cited code and affected callers. Try to refute reachability, impact, and ownership by this diff. Reject it if it is pre-existing, intended, speculative, automatically caught before merge, or unsupported by the cited rule. End with exactly:
> `VERDICT: real | false-positive`
> `CONFIDENCE: 0-100`
> `WHY: <one line citing file:line evidence>`

Keep only `real` findings with confidence at least 80. Uncertainty is a rejection, not a lower-severity finding.

## Phase 4: Report

Lead with findings, ordered by severity, with `file:line` references. For each include the failure scenario, why this diff causes it, verifier confidence, and the smallest concrete fix. Then list verification gaps, if any.

End with `ready to merge` or `needs work`. If nothing survives, say `No high-confidence issues found` and do not manufacture suggestions.
