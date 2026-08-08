---
name: commit
description: Create a focused git commit from the intended changes while preserving unrelated work, checking the exact staged diff, and matching the repository's message conventions. Use when the user asks to commit, save, or checkpoint completed local work without pushing or rewriting existing commits.
---
# Commit Changes

Create a git commit for the current changes.

## Steps

1. Run `git status --short`, inspect staged and unstaged diffs, and read recent commit subjects in parallel.
2. Separate changes that belong to the requested work from unrelated user changes. Never stage an unrelated file, secret, local configuration, generated artifact, or untracked file merely because it is present.
3. If the intended commit cannot be separated safely, ask the user instead of sweeping everything in.
4. Stage explicit paths, preserving any pre-existing staged changes unless the user asks otherwise.
5. Inspect `git diff --cached` as the source of truth. Stop if it is empty or includes unintended content.
6. Draft a message that matches repository style:
   - Summarize the nature (new feature, bug fix, refactor, etc.)
   - Keep it concise (1-2 sentences) focusing on "why" not "what"
   - Do NOT commit files that likely contain secrets (.env, credentials, etc.)
7. Create the commit. Do not push or amend.

Message hint: $ARGUMENTS

If that hint is non-empty, incorporate it into the commit message.

If a hook modifies files, review the modifications and restage only intended paths before retrying. If a hook fails on an in-scope issue, fix it and retry the new commit; never bypass hooks or amend an existing commit. Report the commit hash, subject, and paths committed.
