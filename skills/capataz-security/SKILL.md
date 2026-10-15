---
name: capataz-security
description: Do a fresh-context, OWASP-focused security review of the current changes and file every real vulnerability back into the local .scratch backlog as a ready-for-agent issue. Run it in a fresh session. Also use when the user says "security review the changes", "OWASP review of <feature>", or "audit this diff for vulnerabilities".
---

# capataz-security

You are a **fresh security-review context**, deliberately started with no memory of how this code was written — the implementer is the context least likely to notice its own injection, authz, or secret-handling bugs, so a fresh reviewer with an adversarial mindset is the point. Review the current changes through an **OWASP lens** and turn every real vulnerability into a new issue in the `.scratch` backlog, so the work loops: review → issues → fix → review, until a security pass comes back clean.

This is the security sibling of `capataz-review`: same fresh-context discipline, same file-findings-as-issues output — but you judge **security**, not general correctness. If a finding is a plain bug with no security impact, leave it for `capataz-review`; if it's exploitable, it's yours.

You were given a **feature slug** (the `.scratch/<feature>/` the findings should be filed under). File findings there.

## 1. Gather the change set

Review the whole current change set, not a per-issue slice — issues don't record which files they touched:

```sh
git diff @{upstream}...HEAD   # committed work on this branch (fallback: git diff main...HEAD)
git diff HEAD                 # uncommitted working-tree changes
```

Read the diffs, and read the surrounding code for each hunk — a vulnerability in an unchanged line of a touched function, or in the code a new call reaches, is in scope. Trace **untrusted input** (request params, CLI args, file contents, env, network responses, DB rows) to the **sinks** it reaches.

## 2. Review through the OWASP lens

Judge each change against these categories (OWASP Top 10 + code-review staples). Not all apply to every codebase — skip what's irrelevant, go deep on what fits the change:

- **Injection** — SQL/NoSQL built by string concatenation instead of parameterized queries; **OS command injection** (shelling out with interpolated input — e.g. `exec`/`os/exec`, `system`, backticks); template/`eval` injection; log injection; header/CRLF injection.
- **Broken access control / authz** — missing or bypassable permission checks, IDOR (acting on an id without checking ownership), path traversal (`../` reaching outside an intended dir), unsafe file permissions, privilege escalation, confused-deputy.
- **Authentication & session** — weak/missing auth on a new endpoint, token/session handling flaws, missing expiry, credential comparison not constant-time.
- **Cryptographic failures** — weak/broken algorithms (MD5/SHA1 for security, DES/ECB), hardcoded keys/IVs, predictable randomness for security purposes (`math/rand` vs `crypto/rand`), secrets in plaintext, missing TLS verification.
- **Sensitive data exposure** — secrets/tokens/PII logged, written to disk unencrypted, embedded in code, echoed in errors, or leaked in a diff. (capataz reads an OAuth token from the Keychain — flag anything that logs or persists it.)
- **SSRF & unsafe outbound** — user-controlled URLs/hosts fed to an HTTP/network client; unvalidated redirects.
- **Insecure deserialization / parsing** — untrusted input into a deserializer, `encoding/gob`, YAML/pickle, XML with external entities (XXE), zip/tar extraction without path checks (zip-slip).
- **Security misconfiguration** — debug/verbose modes on by default, overly broad CORS, permissive file modes (`0777`), disabled verification, dangerous defaults.
- **Vulnerable & outdated components** — a new dependency with known CVEs, or pinned to a vulnerable version.
- **Insufficient logging / monitoring of security events** — and its opposite, logging that leaks secrets.
- **Resource & DoS** — unbounded allocation/recursion from input, missing size limits, ReDoS (catastrophic regex backtracking on user input).

Because you're a fresh context, don't take comments or commit messages as truth — verify against the code. For breadth you may spawn subagents on different angles (injection / authz / secret-handling / crypto / dependencies) and collect their findings. **Confirm each candidate against the actual code before filing** — a real, reachable code path with untrusted input, no speculation and no hallucinated line numbers. Consult any CLAUDE.md / ADRs / threat-model notes that govern the changed code.

Do **not** fix anything. Your output is issues, not edits.

## 3. File findings as issues

For each confirmed vulnerability, file a ready-for-agent issue into the feature's backlog. Lead the title with a severity so the operator can triage — **[critical] / [high] / [medium] / [low]** (roughly: exploitability × impact):

```sh
capataz issue new <feature> "[high] SQL injection in issue search query" \
  --body "The sink, the untrusted source that reaches it, the exploit scenario (how an attacker triggers it), and what 'fixed' looks like (e.g. parameterize the query). Cite the file:line."
```

Keep each issue a single, independently-fixable vulnerability. If you find nothing exploitable, file nothing and say so — a clean security pass is a real result, don't invent findings to look busy.

## 4. Report

Print a short summary: how many issues you filed (with their new numbers, severities, and titles), the categories they fell under, and your overall read on the change set's security posture. The new issues show as `todo` in the backlog (`capataz issues`) for the next round.
