---
name: architecture
description: Design or evaluate a system, service, component, API, data model, or architecture decision using repository evidence, explicit constraints, concrete trade-offs, and a reviewable implementation blueprint. Use when the user asks how to architect a change, compare technical options, review a design, define boundaries or contracts, or write an architecture decision record without implementing it yet.
---
# Architecture

Produce a code-grounded design, not a generic best-practices essay. Stay read-only unless the user explicitly asks you to write an ADR or design document. Do not implement the design in this workflow.

Design the following decision or system. If no invocation argument follows, use the user's latest request:

$ARGUMENTS

## Phase 1: Establish requirements

Extract from the request and repository:

- functional behavior and explicit non-goals;
- scale, latency, availability, security, privacy, cost, and operability needs;
- compatibility surfaces such as APIs, CLI output, configuration, persisted data, and migrations;
- team, technology, rollout, and time constraints supported by project evidence.

State assumptions. Ask only about missing information that would materially change the design; otherwise proceed with the safest local convention.

## Phase 2: Ground the design

Launch one or more `explore` agents for disjoint research questions: current execution and data flow, similar components, extension points, storage and protocol boundaries, and existing tests or operational controls. Require file:line evidence and essential files.

Read those essential files yourself. Then launch a `code-architect` agent with the requirements and evidence. Ask for one recommendation that fits the existing system, plus rejected alternatives and the conditions under which they would become preferable.

## Phase 3: Stress the recommendation

Check the proposed design against:

- failure modes, retries, idempotency, partial writes, concurrency, and cleanup;
- trust boundaries, authorization, secret handling, and data retention;
- observability and how operators distinguish healthy, degraded, and failed states;
- versioning, rollout, rollback, migration, and old-client or old-data behavior;
- performance characteristics and where growth changes the design.

Do not invent numerical requirements. If capacity estimates matter, show the variables and assumptions.

## Phase 4: Deliver

Lead with the decision. Include:

1. context, requirements, assumptions, and non-goals;
2. the proposed components, responsibilities, interfaces, and data flow;
3. a compact diagram only when it clarifies three or more relationships;
4. alternatives considered and decisive trade-offs;
5. failure, security, compatibility, and operational behavior;
6. files or modules to create or change, with a phased implementation and test strategy;
7. unresolved decisions and the evidence needed to close them.

If the user requested an ADR, use the repository's existing ADR convention. Otherwise propose `Context`, `Decision`, `Alternatives`, `Consequences`, and `Validation`; write the file only after the path or intent to create one is clear.
