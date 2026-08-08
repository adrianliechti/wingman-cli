---
name: pull-request
description: Prepare, push, create, or update a pull request from the current work with a focused commit history, verified branch diff, accurate rationale, test evidence, and no unrelated or sensitive files. Use when the user asks to open, create, prepare, publish, or refresh a pull request; invocation authorizes the necessary branch push and PR mutation but never force-pushing or merging.
---
# Pull Request

Turn the intended local work into a reviewable pull request. This workflow may create a branch, commit intended changes, push that branch, and create or update its pull request. Never force-push, merge, approve, add labels, or post review comments unless separately requested.

Treat `$ARGUMENTS` as an optional title or intent hint.

## Phase 1: Inspect

In parallel, inspect:

- `git status --short`, staged and unstaged diffs, and relevant untracked files;
- current branch, upstream, remotes, default branch, and merge base;
- recent commit subjects and commits unique to the branch;
- an existing pull request for the branch, when repository tooling is available.

Separate intended work from unrelated user changes. Check the complete branch diff—not only the latest commit—for secrets, generated artifacts, debug output, accidental files, and scope drift. If mixed changes cannot be separated safely, ask before staging or pushing.

## Phase 2: Establish readiness

Read applicable project instructions. Run the narrowest meaningful tests or checks if they have not already passed for the final diff. Stop before publishing when required checks fail, the branch has unresolved conflicts, the change is incomplete, or the diff contains sensitive material. Report the blocker rather than bypassing it.

Review public APIs, CLI behavior, configuration, persisted data, migrations, documentation, rollout, and rollback implications. Capture relevant caveats in the PR body.

## Phase 3: Prepare git history

If on the default branch with uncommitted intended work, create a concise descriptive branch. Stage explicit paths only and inspect `git diff --cached`. Commit using the repository's message style, incorporating `$ARGUMENTS` when useful. Preserve existing commits and staged work; never amend or rewrite history.

If there is nothing new to commit, continue only when the branch already contains commits absent from the base. Push with upstream tracking. Never use a force option.

## Phase 4: Create or update the PR

Derive the title and body from the merge-base diff and current conversation. Preserve useful existing body content, especially issue references, images, migration notes, and externally supplied context.

The body should explain:

1. **Why** the change is needed;
2. **What** changed at the behavioral or architectural level;
3. **How verified**, including exact meaningful tests or manual checks;
4. **Risk and rollout**, including compatibility, migration, feature flags, and rollback when relevant.

Describe only the net change. Do not mention abandoned approaches, absolute local paths, secrets, or confidential context. Use repository-relative paths and issue references. Create a new PR when none exists; otherwise update it only where the current title or body is stale.

## Phase 5: Report

Return the PR link, branch and base, commits pushed, checks run and results, and any reviewer-visible risk or manual verification still outstanding.
