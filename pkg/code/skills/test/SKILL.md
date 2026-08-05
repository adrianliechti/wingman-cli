---
name: test
description: Design, add, repair, or run focused tests for a change or subsystem using the repository's existing test architecture, concrete behavioral risks, meaningful assertions, and proportionate verification.
when-to-use: When the user asks what or how to test, requests regression or integration coverage, wants tests written or fixed, or needs confidence that changed behavior works beyond compilation.
arguments: [scope]
---
# Test

Use `${scope}` to identify the change, behavior, file, or subsystem. If empty, prefer the current diff and the user's latest request.

Choose the mode from the request:

- **Plan**: inspect and return a prioritized test strategy without editing.
- **Add or repair**: write focused executable tests, then run them.
- **Run**: execute the narrowest meaningful existing checks and diagnose failures without changing code unless asked.

## Phase 1: Understand behavior and conventions

Read the implementation branches and boundaries before writing tests. Inspect applicable instruction files, nearby tests, shared fixtures, test helpers, CI commands, and package scripts. Identify the correct test layer: unit for isolated logic, integration for component contracts, end-to-end for critical user flows, and characterization for risky legacy behavior.

Do not introduce a new framework when the repository already has one. Do not copy a large fixture when an existing builder or helper expresses the same setup.

## Phase 2: Build a risk-based matrix

Cover only meaningful risks:

- primary success behavior;
- boundary and representative edge inputs;
- important error, cancellation, retry, and cleanup paths;
- state transitions, persistence, concurrency, or idempotency when relevant;
- public API, protocol, serialization, migration, or compatibility contracts;
- the exact regression trigger for a reported bug.

Skip tests of constants, trivial accessors, compiler-enforced properties, framework behavior, and implementation details that can change without affecting users. Prefer whole-object or observable-output assertions over scattered field checks. Every test should fail for a clear behavioral reason.

For a broad, stateful, or cross-component scope, delegate test construction to `test-engineer` with explicit file boundaries and expected behaviors. Write narrow tests directly.

## Phase 3: Execute proportionately

Run the smallest command that exercises the new or requested coverage. If it passes, broaden only when shared code, protocols, persistence, or build configuration changed. Do not install dependencies without permission and do not hide flaky or failing tests by skipping them.

When a failure reveals a production bug, preserve the failing evidence and report it. Fix production code only if the user requested the behavior fixed, then rerun every check invalidated by the change.

## Report

List behaviors covered, tests added or changed, exact commands and results, failures or flakes found, and important risks intentionally left to another layer or manual verification. Do not substitute a coverage percentage for behavioral confidence.
