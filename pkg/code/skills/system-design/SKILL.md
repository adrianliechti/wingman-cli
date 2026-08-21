---
name: system-design
description: Design a new service, subsystem, platform capability, API, or data flow from requirements and constraints. Use when the user asks to design a system, choose service boundaries, model APIs or data ownership, plan storage, caching, queues, scaling, or reliability, or compare implementation approaches for a substantial new capability. Use architecture instead for reverse-engineering or reviewing an existing repository, generating C4 documentation, or writing an ADR around a focused decision.
---
# System Design

Design a concrete system that fits the user's requirements and the repository's existing constraints. Stay read-only unless the user explicitly asks for a design document. Do not implement the design in this workflow.

For every subagent launched in this workflow, set `model: plan` so discovery and synthesis use the configured frontier model.

Design the following system. If no invocation argument follows, use the user's latest request:

$ARGUMENTS

## Phase 1: Frame the problem

Establish:

- primary users, core flows, and explicit non-goals;
- scale units, latency, availability, consistency, privacy, security, cost, and operability needs;
- existing APIs, data, infrastructure, deployment, compatibility, team, and delivery constraints;
- assumptions whose answers would materially change the design.

Ask only for consequential missing information. Do not invent traffic numbers or service-level objectives; use variables and bounded scenarios when estimates matter.

## Phase 2: Ground the design

When a repository exists, launch `explore` agents with `model: plan` for disjoint questions: reusable components and conventions, current runtime and deployment model, integrations, storage, protocols, and operational controls. Require file:line evidence and read the essential files yourself.

Launch a `code-architect` agent with the requirements and evidence. Ask for one recommended design, credible alternatives, decisive trade-offs, and the conditions that would change the recommendation.

## Phase 3: Resolve the important decisions

Deepen only dimensions that affect the recommendation:

- component responsibilities, dependency direction, and ownership of each datum;
- API contracts, versioning, idempotency, pagination, and error semantics;
- data model, consistency boundaries, storage access patterns, indexes, and retention;
- cache ownership, invalidation, and acceptable staleness;
- synchronous versus asynchronous flow, including ordering, deduplication, delivery, retries, backpressure, and poison work;
- load unit, state growth, likely bottleneck, horizontal or vertical scaling limit, and failover behavior;
- trust boundaries, authorization, secrets, abuse controls, and sensitive-data handling;
- observability, degraded modes, rollout, migration, rollback, and compatibility.

Omit irrelevant dimensions instead of completing a generic template. Prefer the simplest design that meets evidenced requirements; every service, queue, cache, and data copy must justify its operational cost.

## Phase 4: Deliver

Lead with the recommendation. Include:

1. requirements, assumptions, constraints, and non-goals;
2. components, responsibilities, interfaces, and end-to-end data flow;
3. API and data decisions that establish important contracts;
4. a compact diagram only when it clarifies three or more relationships;
5. alternatives rejected and the trade-offs that decided the choice;
6. failure, security, scaling, compatibility, and operational behavior;
7. a phased implementation and behavioral test strategy;
8. unresolved decisions and concrete signals that should trigger revisiting the design.

Do not disguise uncertainty with false precision. Distinguish requirements from assumptions and repository evidence from recommendations. Avoid ornamental scores, exhaustive inventories, and speculative future infrastructure.
