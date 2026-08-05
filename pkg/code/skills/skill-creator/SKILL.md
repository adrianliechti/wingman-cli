---
name: skill-creator
description: Create or improve an Agent Skill with a focused SKILL.md and optional scripts, references, assets, templates, or examples. Use when the user asks to add, scaffold, audit, or refine a reusable skill.
---

# Skill Creator

Create a small, portable skill that teaches one repeatable workflow.

## Workflow

1. Inspect the target repository and any existing skill before writing files. Confirm the user goal, trigger conditions, expected inputs, and successful output from available context; ask only when a missing choice would materially change the workflow.
2. Read [references/skill-format.md](references/skill-format.md). Use [assets/SKILL.template.md](assets/SKILL.template.md) as a starting point when creating a new skill.
3. Keep `SKILL.md` concise. Put essential routing and procedure in the body, and move detailed policies, schemas, examples, or domain background into directly linked files under `references/`.
4. Add supporting files only when they improve reliability:
   - `scripts/` for deterministic computation or file processing.
   - `assets/` for files the workflow copies, transforms, or uses as input.
   - `references/` for material the agent should load selectively.
   - Other clearly named directories such as `templates/` or `examples/` when they communicate purpose better.
5. Reference every necessary supporting file from `SKILL.md` and state when to read, copy, or run it. Use paths relative to the skill directory.
6. Prefer instructions and existing tools over a script. When a script is justified, make its runtime and dependencies explicit in the skill; do not assume the host creates a virtual environment or installs packages automatically.
7. For a skill with Python helpers, optionally reuse [assets/.gitignore](assets/.gitignore) to keep local environments and bytecode out of version control.
8. Validate the result: parse the frontmatter, verify every linked relative path exists, inspect scripts for unsafe or undocumented side effects, and exercise at least one representative invocation when practical.

## Quality bar

- One recognizable user goal per skill.
- A description that says both what the skill does and when it should activate.
- Imperative, testable instructions with explicit stopping conditions.
- Progressive disclosure: load only the references needed for the current task.
- No duplicated prose between `SKILL.md` and references.
- No generated documentation or ornamental assets that the workflow never uses.

When updating an existing skill, preserve useful conventions and unrelated user-authored resources. Report the files added or changed and any runtime prerequisites.
