---
name: triage
description: Verify, deduplicate, rank, and route raw security findings from VULN-FINDINGS.json, scanner output, or a markdown report, then write TRIAGE.json and TRIAGE.md. Use when asked to validate scanner output, prioritize vulnerabilities, reduce false positives, review a security backlog, or prepare findings for patching.
---
# Security Triage

Turn raw security findings into a short list of verified, ranked, owned issues. Prefer `VULN-FINDINGS.json` from `/vuln-scan`, but accept a JSON file, JSONL file, directory of reports, or markdown report.

Arguments: `$ARGUMENTS`. Parse them yourself:
- first positional: findings path (required);
- `--repo PATH`: target repository/source root (default from findings `target`, else `.`);
- `--votes N`: independent verifier votes per candidate (default 3; use an odd number);
- `--keep-ties`: keep split votes as `needs_manual_test` instead of dropping them;
- `--auto`: do not ask clarifying questions; use defaults.

Do not build, run, install, fuzz, send requests, or use the network. Verification is source reading only. You may write only `TRIAGE.json` and `TRIAGE.md`.

## Phase 0: Parse inputs

Parse `$ARGUMENTS`. If no findings path was provided, ask for one. Infer repo path from `--repo`, then from the findings target when present; otherwise use `.` and state that assumption. Reject unknown flags instead of silently treating them as paths.

Recognized fields:

| Canonical | Also accept |
|---|---|
| `id` | `rule_id`, `check_id` |
| `file` | `path`, `filename`, `location.file` |
| `line` | `line_number`, `lineno`, `location.line` |
| `category` | `type`, `cwe`, `kind` |
| `severity` | `level`, `priority` |
| `confidence` | `score`, `triage_confidence` |
| `title` | `name`, `summary`, `message` |
| `description` | `details`, `body`, `rationale`, `evidence` |
| `exploit_scenario` | `attack`, `impact` |
| `recommendation` | `fix`, `remediation`, `mitigation` |

Drop findings with no source location into `skipped` with reason `no source location`.

## Phase 1: Context

Establish verification context before judging reachability:

- Environment: internet-facing service, internal authenticated service, library/SDK, CLI/batch, embedded, or unknown.
- Trust boundary: where attacker-controlled input enters.
- Threat model: if `THREAT_MODEL.md` exists in the repo or target path, read it and carry its threats into ranking.
- Noise policy: default to precision. A finding survives only with majority true-positive votes unless `--keep-ties` was set.

If the environment is unknown, assume externally reachable entry points are untrusted, but flag trust-boundary assumptions in rationale.

## Phase 2: Normalize and dedupe

Normalize all inputs to `findings[]` with stable ids `F-001`, `F-002`, ... in source order. Cluster duplicates by:

- same file and line plus same category;
- same vulnerable helper reached through multiple call sites;
- same missing control across equivalent endpoints.

Keep the clearest representative and record duplicate ids in `duplicates`.

## Phase 3: Adversarial verification

For each candidate, launch the configured number of independent `security` verifiers in parallel. They must not see each other's reasoning. Use this brief:

```text
You are a skeptical security engineer. Default assumption: this finding is WRONG.

Finding:
{candidate}

Environment and trust boundary:
{context}

Tasks:
1. Re-read the cited code yourself.
2. Trace reachability backward from the sink. Can attacker-controlled input, under the stated trust boundary, actually reach it? Quote file:line evidence.
3. Hunt for protections: validation, parameterization, auto-escaping, type or length bounds, auth gates, dead/test code, generated code, or intended design.
4. Stress-test each protection. Does it hold on every relevant path?
5. Apply the exclusion list exactly.

Exclusions:
- Volumetric DoS/rate limiting/resource exhaustion, unless algorithmic blowup, ReDoS, or unbounded recursion is driven by untrusted input.
- Memory-safety claims in memory-safe code outside unsafe/FFI.
- Intended behavior or best-practice gaps with no concrete exploit path.
- Test files, fixtures, generated examples, docs, and markdown.
- Env vars and CLI flags as attack vectors when operator-controlled.
- Outdated dependency versions without a reachable vulnerable call path.
- XSS in auto-escaping frameworks unless a raw-HTML escape hatch is used.
- Missing audit logs, log spoofing, regex injection, open redirect, missing CSRF on stateless/JWT APIs.
- SSRF that controls only path, not host or protocol.
- User input flowing into an LLM prompt without a concrete downstream capability.

End with exactly:
VERDICT: TRUE_POSITIVE | FALSE_POSITIVE | CANNOT_VERIFY
CONFIDENCE: 0-10
WHY: 2-4 sentences citing file:line evidence
```

Keep a finding only if a majority returns `TRUE_POSITIVE`. A tie or majority `CANNOT_VERIFY` is dropped by default and recorded with the verifier rationales. If `--keep-ties` was set, keep split votes as `needs_manual_test`, never as confirmed true positives.

## Phase 4: Severity and ownership

Derive severity from exploitability, not category name:

- HIGH: unauthenticated remote or cross-tenant impact with 0-1 preconditions; RCE, auth bypass, sensitive data exposure, write/privilege escalation.
- MEDIUM: authenticated or 1-2 meaningful preconditions with significant impact.
- LOW: local-only, 3+ preconditions, narrow impact, or defense-in-depth around a real reachable flaw.

A matching `THREAT_MODEL.md` threat may raise severity one step, never two. Assign `owner_hint` from path/module conventions when obvious; otherwise use `unknown`.

## Phase 5: Write artifacts

Write `TRIAGE.json`:

```json
{
  "schema": "wingman.triage.v1",
  "source": "<findings path>",
  "repo": "<repo>",
  "triage_context": {},
  "findings": [],
  "dropped": [],
  "skipped": []
}
```

Each surviving finding includes: `id`, `file`, `line`, `category`, `severity`, `confidence`, `title`, `description`, `exploit_scenario`, `recommendation`, `owner_hint`, `verifier_votes`, and `duplicates`.

Write `TRIAGE.md` sorted by severity then confidence, with one section per survivor:

```markdown
## [SEVERITY] ID title: file:line
- Confidence: N/10
- Preconditions / access: ...
- Owner hint: ...
- Description: ...
- Exploit scenario: ...
- Recommendation: ...
```

End with counts: true positives, false positives, cannot verify, skipped. If no finding survives, state "No verified vulnerabilities found."
