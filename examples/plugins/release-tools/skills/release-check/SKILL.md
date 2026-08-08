---
name: release-check
description: Walk the release checklist for a version before tagging it. Use when preparing or verifying a release.
---

# Release Check

Verify the release named in `$ARGUMENTS` (default: the current `HEAD`).

1. Confirm the working tree is clean and the branch is up to date with its remote.
2. Run the full test suite and report failures verbatim — never summarize a failure away.
3. Check that the version in the project manifest matches the tag being prepared.
4. Read `${SKILL_DIR}/../../templates/changelog.md` and confirm the changelog has an
   entry for this version.
5. Report what passed, what failed, and what you could not check.

Stop and report if any step fails; do not tag a release yourself.
