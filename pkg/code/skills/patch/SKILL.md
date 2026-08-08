---
name: patch
description: Generate minimal, behavior-preserving fixes for verified security findings, then verify each fix builds, passes tests, and closes the root cause. Use when asked to fix, patch, or remediate confirmed vulnerabilities after triage or security review, not raw scanner output.
---
# Patch

Produce the smallest correct fix for each confirmed vulnerability. A patch that breaks legitimate input, changes unrelated behavior, or papers over the symptom is worse than no patch. Fix the root cause, change as little as possible, and prove the fix before claiming it.

`$ARGUMENTS` is preferably `TRIAGE.json` or `TRIAGE.md` from `/triage`. It may also be a `SECURITY-FINDINGS.md` from `/security-review`, or a single described verified finding. **Only patch confirmed, verified findings.** If handed raw scanner output, stop and point the user at `/triage` first.

## Step 1 -- Load and order

Read `$ARGUMENTS`. Take confirmed findings, highest severity first. For each, note the `file:line`, root cause, exploit scenario, and recommended fix from triage. Group findings that share a root cause, such as one vulnerable helper called from many sites. Fix the shared cause once rather than each call site.

## Step 2 -- Understand before editing

For each finding (or root-cause group), read the surrounding code and its callers so the fix fits the codebase's existing patterns:
- What does *legitimate* input to this path look like? The fix must keep accepting it.
- Is there already a sanitizer, validator, or safe wrapper used elsewhere for this class? Reuse it rather than inventing a new one.
- What's the trust boundary the fix should enforce, and is this the right layer to enforce it?

## Step 3 -- Write the minimal fix

Apply the smallest change that closes the hole at its root:
- Parameterize the query; bounds-check before the `memcpy`; validate/normalize at the boundary; use the constant-time compare; escape at the sink; add the missing auth gate.
- Preserve behavior for all legitimate inputs. Do not reformat, rename, or refactor unrelated code in the same edit; keep the diff reviewable and the security change obvious.
- Match the file's existing style and error-handling conventions. No explanatory comments unless the fix encodes a non-obvious invariant a future reader would otherwise undo.

## Step 4 -- Verify each fix

A fix is not done until it's proven. For each patched finding:
1. **Builds & tests pass** -- use a `verification` agent to run the project's build and test commands. If it breaks, fix the regression before moving on.
2. **The hole is closed** -- re-trace the exploit path from the finding: the malicious input that triggered it is now rejected or rendered harmless. State the specific reason it can no longer reach the sink.
3. **Legitimate input still works** -- confirm a valid input on the same path is unaffected. Add or run a test where one naturally fits.
4. **Independent review** -- launch a `code-reviewer` agent with the diff and original finding. Ask it to look only for behavior regressions, incomplete remediation, and over-broad changes. Fix any real issue it reports.

If a fix cannot be verified statically and needs a runtime PoC to confirm, say so and recommend the user verify before merging. Do not claim a fix you could not prove.

## Step 5 -- Report

For each finding: the `file:line` patched, a one-line description of the fix and why it closes the root cause, and the verification result (build/tests, hole-closed reasoning, legitimate-input check). List anything you intentionally did NOT patch (false positives, needs-manual-test, out-of-scope) with the reason. End with the overall diff summary and a recommendation on review/merge.
