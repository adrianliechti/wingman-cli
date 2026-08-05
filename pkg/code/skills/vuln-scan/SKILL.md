---
name: vuln-scan
description: Static source-code vulnerability scan that maps focus areas, fans out read-only security agents, and writes VULN-FINDINGS.json plus VULN-FINDINGS.md for triage.
when-to-use: When asked to scan code for vulnerabilities, find security bugs, produce raw security findings, or run the first stage before triage.
---
# Vulnerability Scan

Run an authorized static scan and write raw findings for `/triage`. This is read-only with respect to target code: do not build, run, install dependencies, send requests, or probe services. You may write only `VULN-FINDINGS.json` and `VULN-FINDINGS.md` in the current workspace.

Arguments: `${ARGUMENTS}`. Parse them yourself:
- first positional: target path (default `.`);
- `--focus TEXT`: add or restrict to a focus area; may appear more than once;
- `--single`: skip fan-out and do one pass;
- `--no-score`: skip Phase 4 confidence calibration.

Reject unknown flags instead of silently treating them as paths.

## Phase 1: Scope

1. Resolve the target path and count source files.
2. If `<target>/THREAT_MODEL.md` exists, read its entry-points, trust-boundaries, and threats tables.
3. If `--focus` was provided, use exactly those focus areas.
4. If no threat model or `--focus` exists, do quick recon with an `explore` agent: entry points, trust boundaries, source languages, frameworks, dangerous sinks, and 3-10 focus areas of the form `<subsystem> (<file/function>) -- <key operations>`.
5. State the source-file count, assumed environment, trust boundary, and focus areas before scanning.

If the target path has no source files, stop with a clear error.

## Phase 2: Fan out

Unless the target is tiny (<15 source files) or `--single` was set, launch one `security` agent per focus area in parallel. Cap at 10 agents. On tiny targets, run a single `security` pass.

Each agent gets this brief:

```text
Authorized static security review. Target: {path}. Focus area: {focus_area}.

Do not build, run, install, fuzz, send requests, or make network calls. Reason from source only.

Report candidate vulnerabilities with a plausible exploit path. If unsure, report with low confidence; later triage verifies rigorously. Skip style issues, generic hardening, and theoretical concerns with no attack story.

High-value classes:
- Memory safety in C/C++/unsafe/FFI: buffer overflow, use-after-free, double-free, integer overflow feeding allocation/index, format string, untrusted recursion/allocation.
- Injection and code execution: SQL/NoSQL/command/LDAP/XPath/template injection, path traversal, unsafe deserialization, eval.
- Auth, crypto, and data: auth/authz bypass, privilege escalation, TOCTOU on security checks, hardcoded secrets, broken crypto/cert validation, secrets or PII in logs/errors.
- Web/UI only when relevant: XSS through raw-HTML escape hatches, SSRF controlling host/protocol, CSRF on stateful cookie auth.

Common false positives to skip:
- Volumetric DoS/rate limiting/resource exhaustion, unless algorithmic blowup, ReDoS, or unbounded recursion is driven by untrusted input.
- Memory-safety claims in memory-safe code outside unsafe/FFI.
- Test files, fixtures, docs, generated code, and examples.
- Missing hardening with no concrete exploit path.
- Env vars and CLI flags as the attack vector when operator-controlled.
- Outdated dependency versions without a reachable vulnerable call path.
- XSS in auto-escaping frameworks unless a raw-HTML escape hatch is used.

For each finding, trace untrusted input entry -> path to sink -> trigger condition.

Output one block per finding:
<finding>
<file>relative/path</file>
<line>line_number</line>
<category>category</category>
<severity>HIGH|MEDIUM|LOW</severity>
<confidence>0.0-1.0</confidence>
<title>one line</title>
<description>root cause and data flow with line references</description>
<exploit_scenario>concrete attack input/source/effect</exploit_scenario>
<recommendation>specific fix</recommendation>
</finding>

If nothing is reportable, say what you covered and return no finding blocks.
```

## Phase 3: Collate

1. Parse all `<finding>` blocks.
2. Drop malformed blocks that lack file or line, recording them in `skipped`.
3. Lightly dedupe candidates with the same `file:line` and category; keep the clearest description and record duplicate ids.
4. Assign stable ids `F-001`, `F-002`, ... sorted by confidence desc, then severity HIGH/MEDIUM/LOW, then file and line.

## Phase 4: Confidence calibration

Skip this phase if `--no-score` was set. Otherwise, for each candidate, launch one shallow `security` verifier in parallel:

```text
Score one raw security finding. Do not execute code or use the network.

Re-read the cited file around the line. Does the code actually do what the finding claims? Check for common false positives and existing protections. Do not drop the finding; only score likely triage survival.

Return exactly:
CONFIDENCE: 1-10
REASON: one line
```

Normalize score to `0.0-1.0` as `triage_confidence`, attach `confidence_reason`, and re-sort.

## Phase 5: Write artifacts

Write `VULN-FINDINGS.json`:

```json
{
  "schema": "wingman.vuln_findings.v1",
  "target": "<path>",
  "environment": "<assumption>",
  "trust_boundary": "<assumption>",
  "focus_areas": [],
  "findings": [],
  "skipped": []
}
```

Write `VULN-FINDINGS.md` with the same findings in human-readable form. End by telling the user the counts and that `/triage VULN-FINDINGS.json --repo <target>` is the next step.
