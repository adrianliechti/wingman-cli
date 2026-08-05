---
name: threat-model
description: Build a threat model for a codebase — map assets, entry points, trust boundaries, and the threats that matter — and write THREAT_MODEL.md to focus later scanning and triage.
when-to-use: When the user asks to threat-model a system, map the attack surface, or decide what to worry about in a codebase. Run this before security-review to scope it.
arguments: [path]
---
# Threat Model

A threat model answers "what could go wrong with this system, who would do it, and what should we do about it?" — independently of any specific bug. It is the map that tells `security-review` where to look and which findings matter.

**Litmus test:** if patching one line makes an entry disappear, it was a vulnerability, not a threat. "Attacker achieves RCE via untrusted media parsing" is a threat; "`dr_wav.h:412` doesn't bounds-check `chunk_size`" is a vulnerability. This skill produces threats; vulnerabilities appear only as *evidence* that raises a threat's likelihood.

**Read-only except for the output file.** Read source, git history, and any vuln reports the user supplies; write only `<path>/THREAT_MODEL.md`. Do not build, run, or fuzz. Use `explore` and `security` agents for the research swarm. Default `${path}` to the current directory.

## Step 1 — Pick a mode

- **bootstrap** (default): derive the model from the code itself. Best for inherited/third-party code, or when no system owner is in the session.
- **interview**: if the user is the system owner and present, walk the four questions below with the `elicit` tool, then ground their answers in the code. Best for new systems where the risk lives in business logic the code doesn't show.
- **bootstrap-then-interview**: bootstrap a draft first, then walk it with the owner. Use when both code and owner are available — the owner refines a code-grounded draft instead of starting cold.

The four interview questions: (1) What are we working on? (2) What can go wrong? (3) What are we going to do about it? (4) Did we do a good job?

## Step 2 — Bootstrap research swarm

Launch agents in parallel, each covering one area, returning structured notes:
- **Assets & data**: what's valuable — secrets, PII, money, integrity of outputs, availability.
- **Entry points & trust boundaries**: every place untrusted input enters (network, files, IPC, CLI on multi-tenant hosts, deserialization) and the boundary it crosses.
- **Actors**: who interacts with the system and at what privilege; who a worst-case attacker is.
- **History**: `git log` for security-relevant changes; any supplied CVEs, pentest reports, or `vulns.txt`. Generalize past bugs into threat *classes*.

Then do a STRIDE gap-fill pass (Spoofing, Tampering, Repudiation, Information disclosure, Denial of service, Elevation of privilege) to catch threats the code alone didn't surface.

## Step 3 — Write THREAT_MODEL.md

Write `<path>/THREAT_MODEL.md` with these sections so downstream skills can parse it:

1. **Context** — what the system does, in two or three sentences.
2. **Assets** — what must be protected.
3. **Entry points & trust boundaries** — a table: `entry point | input source | trust boundary crossed`.
4. **Threats** — a table: `id | threat | actor | attack surface | asset at risk | impact | likelihood | status | controls`. Rank by impact × likelihood.
5. **Deprioritized** — threats consciously accepted, with why.
6. **Open questions** — what the code couldn't answer (these seed an interview pass).

## Step 4 — Hand back

Tell the user: the path written, the top 5 threats by impact × likelihood (id + one line each), and the open questions. Next step: `security-review ${path}` will use the entry-points and threats tables to focus its scan.
