---
name: memory
description: Save, revise, or remove durable facts in persistent per-project memory using the normal file tools. Use when the user asks to remember or forget something, shares durable facts or preferences, corrects the agent's approach, or when a relevant memory may already exist.
---
# Memory

You have a persistent, file-based memory at the path shown in the Memory section of the system prompt (typically `~/.wingman/projects/{cwd}/memory/`). Memory survives across conversations: an index of every `*.md` file in that directory is auto-generated and injected into the system prompt of every future session, using each file's frontmatter `description` as the one-line hook. So anything you `write` there is automatically available next time.

You manage memory with the **normal file tools** — `write`, `edit`, `read`, `glob`. The memory directory is an allowed write root, so workspace-relative path rules don't apply: pass the absolute path inside the memory directory.

If the user asks you to remember something, save it. If they ask you to forget something, find and delete it.

## When to save

- After any "remember this" / "from now on" instruction.
- When the user corrects your approach ("no, don't do that") OR validates a non-obvious one ("yeah the bundled PR was the right call here"). Save from both — saving only corrections makes you overly cautious; saving validated calls preserves judgment.
- When you learn durable facts about the user (role, expertise, preferences, what they're focused on).
- When you learn project context that isn't in the code: who is doing what, why, by when; what motivates an in-flight rewrite; what deadline is approaching.
- When the user references an external system worth pointing at later (Linear project, Slack channel, Grafana dashboard, runbook).

## What NOT to save

These exclusions hold even when the user asks you to save them — instead, ask what was *surprising* or *non-obvious* about them and save that.

- Code patterns, conventions, architecture, file paths, project structure — re-derivable by reading the repo.
- Git history, recent diffs, who-changed-what — `git log` / `git blame` are authoritative.
- Debugging recipes — the fix is in the diff; the commit message has the context.
- Anything already in AGENTS.md / CLAUDE.md.
- Ephemeral conversation state — in-progress task details, current scratch context.

## Memory types

- **user** — the user's role, goals, responsibilities, knowledge, stable preferences. Lets you tailor explanations to who they actually are.
- **feedback** — guidance about how to approach work. Lead with the rule, then `**Why:**` (the reason the user gave) and `**How to apply:**` (when this kicks in) so future-you can judge edge cases.
- **project** — in-flight initiatives, decisions, bugs, incidents, deadlines, stakeholders. Convert relative dates to absolute ones ("Thursday" → "2026-03-05") so the memory stays interpretable. Lead with the fact, then `**Why:**` and `**How to apply:**`.
- **reference** — pointers to where information lives in external systems.

## File shape

Each memory is one markdown file with a tiny YAML frontmatter and a body. The frontmatter `description` becomes the one-line hook in the auto-generated index — keep it under ~150 chars and specific enough that future-you can decide relevance at a glance.

```
---
name: feedback_testing
description: integration tests must hit a real database; no mocks
type: feedback
---

Integration tests must hit a real database, not mocks.

**Why:** prior incident where mock/prod divergence masked a broken migration.
**How to apply:** any test under `internal/db/...` or that exercises a query path.
```

Filename is `{name}.md` — lowercase letters, digits, underscore, hyphen. Group semantically: `user_role.md`, `feedback_testing.md`, `project_auth_migration.md`. Cap files at ~25 KB.

## Workflow recipes

**Save a new memory.** Single call: `write` to `{memory_dir}/feedback_testing.md` with frontmatter + body. The framework picks up the new file on the next turn — no separate index to maintain.

**Update an existing memory.** Use `edit` on `{memory_dir}/{name}.md` for surgical changes. If the rule has changed, update the `description` in the frontmatter too so the index reflects the new gist.

**Forget a memory.** Use `shell` `rm {memory_dir}/{name}.md`. The index updates automatically on the next turn. If you only need to revise the fact, edit the file instead of deleting.

**List what's remembered.** `glob` `*.md` inside the memory dir; or just consult the index already injected at the top of the system prompt.

**Inspect a specific memory.** `read` with the absolute path inside the memory dir.

## Before recommending from memory

A memory that names a function, file, or flag is a claim that it existed *when the memory was written*. Before acting on it: verify the file still exists, grep for the symbol, sanity-check the constraint still holds. Trust what you observe now over what the memory says — and update or remove the stale memory rather than acting on it.

A memory that summarizes repo state is frozen in time. If the user asks about *recent* or *current* state, prefer fresh `git log` / `read` over the snapshot.

## Memory vs. other persistence

- Use **tasks** (not memory) for in-conversation progress tracking.
- Use a **plan** (not memory) to align on approach for the current task.
- Use **memory** only for things that should outlive this conversation.
