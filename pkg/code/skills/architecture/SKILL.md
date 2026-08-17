---
name: architecture
description: Design, evaluate, review, or reverse-engineer a system architecture using repository evidence, explicit constraints, and concrete trade-offs. Use when the user asks how to architect a change, compare technical options, review architecture health, boundaries, contracts, or technical debt, write an ADR, understand an existing codebase, or generate C4-style Mermaid context, container, component, dynamic, or deployment diagrams.
---
# Architecture

Produce a code-grounded design or architecture model, not a generic best-practices essay. Stay read-only unless the user explicitly asks you to write an ADR, design document, or diagram file. Do not implement the design in this workflow.

For every subagent launched in this workflow, set `model: plan` so architecture discovery and synthesis use the configured frontier model.

Design the following decision or system. If no invocation argument follows, use the user's latest request:

$ARGUMENTS

## Phase 1: Establish requirements

Extract from the request and repository:

- functional behavior and explicit non-goals;
- scale, latency, availability, security, privacy, cost, and operability needs;
- compatibility surfaces such as APIs, CLI output, configuration, persisted data, and migrations;
- team, technology, rollout, and time constraints supported by project evidence.

State assumptions. Ask only about missing information that would materially change the design; otherwise proceed with the safest local convention.

For an existing-system diagram, treat the invocation as sufficient permission to analyze the repository. Default to the whole repository and a technical audience. Ask for scope only when the repository is too large to model coherently.

## Phase 2: Ground the design

Launch one or more `explore` agents for disjoint research questions: current execution and data flow, runtime/deployment units, external integrations, storage and protocol boundaries, similar components, and existing tests or operational controls. Require file:line evidence and essential files.

Read those essential files yourself. Then launch a `code-architect` agent with the requirements and evidence. Ask for one recommendation that fits the existing system, plus rejected alternatives and the conditions under which they would become preferable.

For a new or changed system, deepen only the decisions that could change the recommendation: data ownership and consistency, API contracts and versioning, storage and indexing, cache ownership and invalidation, or synchronous versus asynchronous flow with ordering, delivery, and backpressure behavior. Omit irrelevant sections instead of filling out a standard system-design template.

## Phase 3: Stress the recommendation

Check the proposed design against:

- failure modes, retries, idempotency, partial writes, concurrency, and cleanup;
- trust boundaries, authorization, secret handling, and data retention;
- observability and how operators distinguish healthy, degraded, and failed states;
- versioning, rollout, rollback, migration, and old-client or old-data behavior;
- performance characteristics and where growth changes the design.

When scale is relevant, identify the unit of load, read/write path, state growth, likely bottleneck, and whether the design scales by replication, partitioning, or a deliberate vertical limit. Do not invent numerical requirements. If capacity estimates matter, show the variables and assumptions.

## Phase 4: Deliver

Lead with the decision. Include:

1. context, requirements, assumptions, and non-goals;
2. the proposed components, responsibilities, interfaces, and data flow;
3. a compact diagram only when it clarifies three or more relationships;
4. alternatives considered and decisive trade-offs;
5. failure, security, compatibility, and operational behavior;
6. files or modules to create or change, with a phased implementation and test strategy;
7. strengths to preserve, concrete architecture risks, and the few highest-value improvements;
8. unresolved decisions and the evidence needed to close them.

If the user requested an ADR, use the repository's existing ADR convention. Otherwise propose `Context`, `Decision`, `Alternatives`, `Consequences`, and `Validation`; write the file only after the path or intent to create one is clear.

## Diagram mode

When the request asks to document or visualize an existing system, reverse-engineer the model from evidence before drawing it:

1. Identify people, external systems, independently runnable or deployable containers, data stores, queues, protocols, and dependency direction. Treat packages and folders as components, not containers.
2. Trace each element and relationship to repository evidence. Mark architecture inferred from naming or conventions as inferred; code rarely proves organizational actors, production topology, or runtime ownership.
3. Choose the smallest useful set of views:
   - Default to one `C4Container` view.
   - Add `C4Context` only when people or external systems are evidenced.
   - Add `C4Component` only for a requested or especially important container; never turn every package into one huge view.
   - Use `C4Dynamic` for a specific runtime flow and `C4Deployment` only when deployment configuration supports it.
4. Give every relationship a meaningful action and protocol when known. Point the arrow from the initiator to the dependency. Omit unsupported connections rather than completing a familiar pattern from memory.
5. Use short stable ASCII identifiers, concise descriptions, and a Mermaid fenced block. Prefer Mermaid's C4 syntax; if the selected renderer rejects a necessary construct, fall back to a `flowchart` with clearly named C4 boundaries instead of changing the model.
6. Follow the diagram with a compact evidence table (`element or relationship`, `evidence`, `confidence`) and an `Unknowns` list. Do not claim the diagram is complete.
7. Add an `Architecture review` after the model:
   - Explain what the current architecture appears to optimize for and name strengths worth preserving.
   - Identify pressure points in boundaries, dependency direction, data ownership, runtime failure handling, trust boundaries, operability, testability, or change hotspots only when repository evidence supports them.
   - Separate demonstrated problems from growth-dependent concerns. For each concern, state the consequence, evidence, and the condition that makes it worth addressing.
   - Recommend at most a few high-leverage changes, ordered by likely impact and confidence. Include the trade-off or new complexity each recommendation introduces.
   - End with signals that should trigger revisiting the architecture, such as a scale threshold, repeated cross-boundary changes, reliability incidents, or a new deployment or ownership model.

Do not turn the review into a generic checklist, scorecard, or inventory of every imperfection. Do not infer team structure, production scale, or operational pain from folder names alone. Avoid ornamental severity and count labels; use priority language only when the evidence shows a materially different consequence or urgency.

Keep each view readable without zooming through dozens of nodes. Output diagrams in chat by default. If the user asks to save one, follow the repository's documentation convention; otherwise propose `docs/architecture.mmd` and never overwrite an existing artifact without confirmation.
