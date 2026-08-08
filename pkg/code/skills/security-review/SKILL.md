---
name: security-review
description: End-to-end read-only security audit that scans focus areas and adversarially verifies candidates so only high-confidence exploitable vulnerabilities are reported. Use when the user wants a concise security audit of a project or path; use vuln-scan plus triage when durable JSON or markdown artifacts are needed.
---
# Security Review

Find HIGH-CONFIDENCE, genuinely exploitable vulnerabilities — not a checklist of theoretical concerns. The discovery pass casts a wide net; a separate adversarial verification pass is what removes false positives. Every reported finding must survive a skeptic who started by assuming it was wrong.

This is **read-only**. Spawn `security` agents for all scanning and verification. Never build, run, install, send requests, or probe the target. If `$ARGUMENTS` contains a path, scope everything to it. If the user asks for raw scanner output, backlog triage, or patch-ready artifacts, use `/vuln-scan` and `/triage` instead.

## Phase 1: Scope

First fix the **environment**, because reachability — and therefore every verdict — is judged against it: is this internet-facing (HTTP is untrusted), an internal service (callers are authenticated peers), a library/SDK (the caller is the trust boundary), or a CLI/batch tool (operator input is trusted, file/network input is not)? State it up front. It decides cases like an env-var- or CLI-driven sink: operator-controlled and excluded in a CLI, but a true positive in a multi-tenant web service.

Then map the project: entry points, trust boundaries, where untrusted input enters, what frameworks/sanitizers are already in use. If a `THREAT_MODEL.md` exists, use its entry points and threat list to focus (and take the environment from it). Otherwise propose 3–10 focus areas of the form `<subsystem> (<file/function>) — <key operations>` and tell the user the environment, focus areas, and source-file count before scanning.

## Phase 2: Find — parallel scanners (wide net)

Launch one `security` agent per focus area, concurrently. On a small target (<15 source files) do a single pass instead. Give each agent its focus area and this brief:

> Authorized static security review; focus area: **{area}**. Reason from the code — do not run anything. Report anything with a plausible exploit path; if unsure, report it with low confidence rather than dropping it (a later pass verifies rigorously). For each finding trace: where untrusted input enters → the path to the sink → the trigger condition.
>
> HIGH VALUE: memory safety in C/C++ or `unsafe`/FFI (buffer overflow, use-after-free, integer overflow feeding an allocation/index, format-string, untrusted-size-driven recursion/allocation); injection (SQL/command/LDAP/XPath/template, path traversal, unsafe deserialization, eval); auth/authz bypass, privilege escalation, TOCTOU on a security check; hardcoded secrets, broken crypto/cert validation; secrets or PII in logs/errors.
>
> LOW VALUE (note briefly, keep looking): null-pointer deref at small fixed offsets with no attacker control; assertion failures or clean error returns — that is correct handling, not a bug.
>
> Report `file:line`, category, severity (HIGH/MEDIUM/LOW), confidence (0–1), a description with the data flow, a concrete exploit scenario, and a fix.

## Phase 3: Verify — adversarial voting

Collapse duplicates first — cluster candidates with the same `file:line` + category (and a shared root cause: one vulnerable helper reported per call site, one missing control reported per endpoint), keep the best-described, and drop the rest so duplicates don't each burn verifiers. Then, for each surviving candidate, spawn **3 independent `security` verifiers in parallel** (one message). They must not see each other's reasoning. Each gets:

> You are a skeptical security engineer. Default assumption: this finding is WRONG. (1) Read the cited code yourself; scanners misread code, and trusting the summary inherits the misread. (2) Trace reachability backward from the sink — can attacker-controlled input (per the environment from Phase 1) actually reach it? Quote the first call site you read. (3) Hunt for protections: validation, parameterization, framework auto-escaping, type/length bounds, auth gates, dead/test code. (4) Stress-test each protection — does it hold on every path?
>
> It is FALSE_POSITIVE even if technically accurate when it matches an exclusion below.
>
> End with exactly:
> `VERDICT: TRUE_POSITIVE | FALSE_POSITIVE | CANNOT_VERIFY`
> `CONFIDENCE: 0-10`
> `WHY: <2-4 sentences citing file:line for reachability and protections>`

**Tally:** keep a finding only if the majority vote is `TRUE_POSITIVE`. A tie or majority `CANNOT_VERIFY` drops it (favor precision). Set its confidence to the mean of the agreeing votes.

### Exclusions (do not report, even if technically present)
DoS / rate-limiting / resource exhaustion (but ReDoS, algorithmic blowup, and untrusted-input-driven unbounded recursion ARE in scope); memory-safety issues in memory-safe languages outside `unsafe`/FFI; behavior that is the intended design; missing hardening or a best-practice gap with no concrete exploit path; secrets stored on disk (managed separately); outdated dependency versions; env vars and CLI flags as the attack vector (operator-controlled) — UNLESS the environment from Phase 1 marks them untrusted; weak random used for non-security purposes (jitter, shuffling, dev fallbacks); identifiers unguessable by construction (UUIDv4, 128-bit+ tokens) flagged as "predictable"; client-side code flagged for a server-side vulnerability class; test files and fixtures; docs/markdown; log spoofing; SSRF controlling only the path (not host/protocol); object-storage `../` that doesn't escape a trust boundary; regex injection; open redirect; missing CSRF on stateless/JWT APIs; missing audit logs; timing attacks on non-crypto operations; XSS in an auto-escaping framework (React/Angular/Vue/Jinja autoescape) unless via a raw-HTML escape hatch (`dangerouslySetInnerHTML`, `bypassSecurityTrustHtml`, `v-html`, `|safe`); user input flowing into an LLM prompt; theoretical-only TOCTOU with no realistic window.

## Phase 4: Rank & report

Derive severity from exploitability, not the category name — list the preconditions and the minimum access level, then take the lower of: `0 preconditions + unauthenticated remote → HIGH`; `1–2 + authenticated → MEDIUM`; `3+ or local-only → LOW`. A matching `THREAT_MODEL.md` threat may raise severity one step (never two). Sort by confidence, then severity. For each:

```
## [SEVERITY] Category: file_path:line_number
- Confidence: 0.XX
- Preconditions / access: <what must hold, and unauthenticated-remote | authenticated | local>
- Description: <root cause and data flow>
- Exploit scenario: <what input, from where, to what effect>
- Recommendation: <specific fix>
```

End with a summary (files reviewed; HIGH/MEDIUM/LOW counts). If nothing survived verification, state "No high-confidence vulnerabilities found." For a confirmed HIGH, recommend a human build a proof-of-concept before relying on the finding — do not write one here.

If the user wants the findings fixed, write the confirmed findings to `SECURITY-FINDINGS.md` (the format above) and hand off to `patch`, which consumes that file. For a deeper engagement, `threat-model` first maps the attack surface to focus this scan.
