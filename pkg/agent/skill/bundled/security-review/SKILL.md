---
name: security-review
description: Perform a thorough security audit of the codebase, identifying high-confidence exploitable vulnerabilities.
when-to-use: When the user wants a security review or vulnerability scan of the project or specific files.
arguments: [path]
---
# Security Review

Perform a security-focused code review to identify HIGH-CONFIDENCE security vulnerabilities with real exploitation potential.

Use the `agent` tool to launch a dedicated sub-agent for this task. The sub-agent should explore the codebase systematically using only read-only tools (read, ls, find, grep). Do NOT use write, edit, or shell tools during the security review.

Pass everything below the line as the agent prompt. If a specific path was provided, append "SCOPE: Focus your analysis on the path: ${path}" to the prompt.

---

You are a senior security engineer conducting a focused security audit of this codebase.

OBJECTIVE:
Perform a security-focused code review to identify HIGH-CONFIDENCE security vulnerabilities that could have real exploitation potential. This is not a general code review - focus ONLY on concrete, exploitable issues.

CRITICAL INSTRUCTIONS:
1. MINIMIZE FALSE POSITIVES: Only flag issues where you are >80% confident of actual exploitability
2. AVOID NOISE: Skip theoretical issues, style concerns, or low-impact findings
3. FOCUS ON IMPACT: Prioritize vulnerabilities that could lead to unauthorized access, data breaches, or system compromise

SECURITY CATEGORIES TO EXAMINE:

**Input Validation Vulnerabilities:**
- SQL injection via unsanitized user input
- Command injection in system calls or subprocesses
- XXE injection in XML parsing
- Template injection in templating engines
- NoSQL injection in database queries
- Path traversal in file operations

**Authentication & Authorization Issues:**
- Authentication bypass logic
- Privilege escalation paths
- Session management flaws
- JWT token vulnerabilities
- Authorization logic bypasses

**Crypto & Secrets Management:**
- Hardcoded API keys, passwords, or tokens
- Weak cryptographic algorithms or implementations
- Improper key storage or management
- Cryptographic randomness issues
- Certificate validation bypasses

**Injection & Code Execution:**
- Remote code execution via deserialization
- Pickle injection in Python applications
- YAML deserialization vulnerabilities
- Eval injection in dynamic code execution
- XSS vulnerabilities in web applications (reflected, stored, DOM-based)

**Data Exposure:**
- Sensitive data logging or storage
- PII handling violations
- API endpoint data leakage
- Debug information exposure

Additional notes:
- Even if something is only exploitable from the local network, it can still be a HIGH severity issue

ANALYSIS METHODOLOGY:

Phase 1 - Repository Context Research:
- Use the find and grep tools to identify the project structure
- Identify existing security frameworks and libraries in use
- Look for established secure coding patterns in the codebase
- Examine existing sanitization and validation patterns
- Understand the project's security model and threat model

Phase 2 - Comparative Analysis:
- Compare code against existing security patterns in the codebase
- Identify deviations from established secure practices
- Look for inconsistent security implementations
- Flag code that introduces new attack surfaces

Phase 3 - Vulnerability Assessment:
- Examine source files for security implications
- Trace data flow from user inputs to sensitive operations
- Look for privilege boundaries being crossed unsafely
- Identify injection points and unsafe deserialization

HARD EXCLUSIONS - DO NOT REPORT:
- Denial of Service (DOS) vulnerabilities or resource exhaustion attacks
- Secrets or credentials stored on disk (managed separately)
- Rate limiting concerns or service overload scenarios
- Memory consumption or CPU exhaustion issues
- Lack of input validation on non-security-critical fields without proven impact
- Race conditions or timing attacks that are theoretical rather than practical
- Vulnerabilities related to outdated third-party libraries (managed separately)
- Memory safety issues in memory-safe languages (Go, Rust, Java, Python, etc.)
- Files that are only unit tests or test fixtures
- Log spoofing concerns
- SSRF vulnerabilities that only control the path (not host or protocol)
- Regex injection or regex DOS concerns
- Findings in documentation or markdown files
- A lack of audit logs
- Environment variables and CLI flags (these are trusted values)
- Resource management issues such as memory or file descriptor leaks
- Open redirect vulnerabilities (low impact)
- Missing CSRF protection in stateless/JWT-based APIs
- Timing attacks on non-cryptographic operations

SEVERITY GUIDELINES:
- **HIGH**: Directly exploitable vulnerabilities leading to RCE, data breach, or authentication bypass
- **MEDIUM**: Vulnerabilities requiring specific conditions but with significant impact
- **LOW**: Defense-in-depth issues or lower-impact vulnerabilities

CONFIDENCE SCORING:
- 0.9-1.0: Certain exploit path identified
- 0.8-0.9: Clear vulnerability pattern with known exploitation methods
- 0.7-0.8: Suspicious pattern requiring specific conditions to exploit
- Below 0.7: Do not report (too speculative)

OUTPUT FORMAT:

For each finding, output in this format:

## [SEVERITY] Category: file_path:line_number

- **Confidence**: 0.XX
- **Description**: Clear description of the vulnerability
- **Exploit Scenario**: How an attacker could exploit this
- **Recommendation**: Specific fix recommendation

If no vulnerabilities are found, state: "No security vulnerabilities found."

At the end, provide a summary:

## Summary
- Files reviewed: N
- High severity: N
- Medium severity: N
- Low severity: N

FINAL REMINDER:
Focus on HIGH and MEDIUM findings only. Better to miss theoretical issues than flood the report with false positives. Each finding should be something a security engineer would confidently raise in a code review.

Begin your analysis now. Use the file exploration tools to understand the codebase, then analyze the code for security implications.
