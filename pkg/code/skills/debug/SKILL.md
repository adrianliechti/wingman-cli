---
name: debug
description: Diagnose unexpected behavior by reproducing it, narrowing the failing path, testing competing hypotheses, and identifying the evidence-backed root cause; implement and verify a fix only when requested.
when-to-use: When the user provides an error, failing test, stack trace, crash, regression, environment mismatch, performance anomaly, or behavior whose cause is not yet known.
arguments: [problem]
---
# Debug

Debug from evidence. Do not make speculative edits, silence the symptom, or change several variables at once. Diagnosis is read-only by default; if the user also asked to fix the problem, continue through remediation and verification.

`${problem}` is the observed failure. If empty, use the user's latest description.

## Phase 1: Define and reproduce

Write down the expected behavior, actual behavior, smallest known reproduction, affected scope, and last known good state. Preserve exact errors, stack traces, inputs, versions, and environment differences.

Run the narrowest safe reproduction. Record the command and outcome. If reproduction would mutate external state, require credentials, or affect production, do not run it; use local evidence and state the limitation.

## Phase 2: Localize

Trace from the observed failure toward its inputs and dependencies. Inspect applicable project instructions, logs, diagnostics, recent changes, configuration, dependency versions, and nearby tests. Compare working and failing paths rather than reading the whole repository.

Use an `explore` agent when the call chain or subsystem is broad. Ask for entry points, data flow, error transformations, relevant history, and file:line evidence—not a proposed fix.

## Phase 3: Test hypotheses

Maintain a short ranked set of mutually distinguishable hypotheses. For each, choose the cheapest observation that could disprove it. Change one variable at a time and prefer existing diagnostics, focused tests, temporary command-line probes, or debugger output over source edits.

Discard contradicted hypotheses. A correlation with a recent change is not a root cause until the failing mechanism is traced. Stop only when one explanation accounts for the trigger, path, and observed outcome, or when the next discriminating observation requires user or external access.

## Phase 4: Fix when requested

Make the smallest root-cause fix that preserves valid behavior. Add or strengthen a regression test that fails for the original reason and passes after the fix. Do not modify production code merely to accommodate an incorrect test.

Use a fresh `verification` agent for a broad or environment-sensitive fix. Re-run the exact reproduction first, then the narrowest relevant test or check, and probe one adjacent failure or boundary case.

## Report

State:

- reproduction and evidence collected;
- root cause with the complete trigger-to-failure chain and file:line references;
- hypotheses rejected and the evidence that rejected them, when useful;
- fix and regression coverage, if requested;
- exact verification results;
- remaining uncertainty or the next discriminating check if no root cause was proven.

Never label a plausible hypothesis as the root cause.
