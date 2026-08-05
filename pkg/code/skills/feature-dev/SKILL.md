---
name: feature-dev
description: Deliver a non-trivial feature by tracing the existing code, resolving material requirements, choosing one fitting architecture, implementing reviewable slices, and proving the behavior with focused tests. Use when a requested change spans components, changes behavior or contracts, introduces a new workflow, or would be risky to implement without first understanding local patterns.
---
# Feature Development

Deliver the feature end to end. Scale the process to the change: a focused feature may need one explorer and a compact blueprint; a cross-cutting feature may need several independent research passes. Do not add ceremony that does not reduce risk.

`${ARGUMENTS}` is the feature or change request. If it is empty, use the user's latest message.

## Phase 1: Establish scope and explore

Read the applicable project instructions and inspect git status before editing. Launch one to three `explore` agents with disjoint questions such as the current execution flow, similar features, extension points, user experience, and test strategy. Give each:

- user request;
- likely paths or symbols, if known;
- project guideline files (`AGENTS.md`, `CLAUDE.md`) that apply;
- request for entry points, execution flow, data flow, key abstractions, similar implementations, compatibility surfaces, tests, and five to ten essential files.

Require file:line evidence and a clear facts-versus-inference distinction. After the agents return, read the essential files yourself; do not design from summaries alone.

## Phase 2: Resolve material ambiguity

Turn exploration into a concrete behavioral contract: inputs, outputs, error behavior, persistence, compatibility, and explicit non-goals. State safe assumptions. Ask the user only when different answers would materially change behavior, data, public contracts, or scope; otherwise proceed with the best-supported local convention.

## Phase 3: Architecture

Launch a `code-architect` agent with the request and explorer findings. Ask for one implementation blueprint, not a menu:

- patterns and conventions found;
- files to create or modify;
- component responsibilities and interfaces;
- data flow;
- error handling, persistence, security, performance, and compatibility concerns;
- focused test strategy;
- phased build sequence.

Include the smallest coherent implementation slices and identify which slice can be verified independently. Reject parallel abstractions that duplicate an existing path. If the blueprint exposes a new material ambiguity, resolve it before editing.

## Phase 4: Implement

Work through the blueprint in small, reviewable phases:

1. Edit the minimum set of existing files first.
2. Add new files only when the existing architecture calls for them.
3. Keep public behavior and APIs stable unless the request explicitly changes them.
4. Follow local style and helper APIs instead of inventing parallel patterns.
5. Leave unrelated refactors for later.

Use `test-engineer` when behavior is broad, stateful, security-sensitive, or regression-prone. For a narrow change, add focused tests directly. Give every mutating subagent a disjoint file scope and explicit non-goals.

## Phase 5: Review and verify

After implementation:

1. Inspect the final diff for accidental scope growth and changes to public APIs, CLI behavior, configuration, persisted data, or migrations.
2. Launch a `code-reviewer` agent on the diff for correctness, silent failures, compatibility, test gaps, guideline violations, and security regressions.
3. Launch a `code-simplifier` agent only if the diff introduces abstractions, repeated logic, or hot-path work.
4. Run the narrowest meaningful build, test, lint, type-check, or direct behavior check. Broaden only when the changed surface warrants it.
5. Fix real issues and rerun checks invalidated by the fix.

## Phase 6: Report

Summarize:

- what changed;
- key files touched;
- tests/checks run and their result;
- any deliberate non-goals, follow-up risks, or manual verification still needed.
