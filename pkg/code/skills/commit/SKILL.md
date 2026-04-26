---
name: commit
description: Stage and commit changes with a well-crafted commit message.
when-to-use: When the user asks to commit, save, or checkpoint their work.
arguments: [message]
---
# Commit Changes

Create a git commit for the current changes.

## Steps

1. Run `git status` to see all changed files
2. Run `git diff` to understand the changes (both staged and unstaged)
3. Run `git log --oneline -5` to match the repo's commit message style
4. Analyze the changes and draft a commit message:
   - Summarize the nature (new feature, bug fix, refactor, etc.)
   - Keep it concise (1-2 sentences) focusing on "why" not "what"
   - Do NOT commit files that likely contain secrets (.env, credentials, etc.)
5. Stage the relevant files (prefer specific files over `git add -A`)
6. Create the commit

${ARGUMENTS}

If the user provided a message hint above, incorporate it into the commit message.

If pre-commit hooks fail, fix the issue and create a NEW commit (never amend).
