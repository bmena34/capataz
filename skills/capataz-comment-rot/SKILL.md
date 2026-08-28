---
name: capataz-comment-rot
description: Pre-PR docs hygiene pass — scrub the README, CLAUDE.md, and code comments touched by the current changes for rot: stale claims, drifted commands, verbose or narrating comments, and references to capataz issues or PRs. READMEs are judged for human readability, CLAUDE.md for agent signal-per-token, code comments for concision and truth. Edits docs directly; never changes code behavior. Invoked before opening a PR, or when the user says "comment rot pass", "scrub the docs", "check the README/comments", or "docs pass before PR".
---

# capataz-comment-rot

You are a **fresh docs context**: you didn't write these changes, so you can judge whether the prose still tells the truth and earns its keep. Before a PR goes up, sweep the documentation the change set touches — README, CLAUDE.md, and code comments — and fix the rot in place. This is a docs-only pass: you edit prose and comments, **never code behavior**.

## 1. Gather the change set

```sh
git diff @{upstream}...HEAD --name-only   # committed work on this branch (fallback: git diff main...HEAD --name-only)
git diff HEAD --name-only                 # uncommitted working-tree changes
```

Your scope is:
- every changed source file's **comments** (read the full file, not just the hunks — a change can rot a comment fifty lines away),
- any **README.md** at the repo root or in a changed directory,
- any **CLAUDE.md** (or other agent-instruction file) at the repo root or in a changed directory.

If the change set is empty, sweep the repo-root README.md and CLAUDE.md only, and say that's what you did.

## 2. Three lenses, one per doc kind

**README — human readability first.** The reader is a person encountering the project cold. Verify every claim, command, flag, and path against the current code — run the commands' `--help` or read the source rather than trusting the prose. Fix what drifted, delete sections describing behavior that no longer exists, and tighten wording. Readability outranks brevity: keep complete sentences and explanatory flow; cut redundancy, not clarity.

**CLAUDE.md — agent signal-per-token.** The reader is an agent with limited context. Every line should either state a non-obvious constraint, map architecture the code can't show at a glance, or prevent a known mistake. Delete lines an agent could derive by reading the code, duplicates of the README, and stale gotchas for code that changed. Keep instructions imperative and unambiguous. Update — don't just delete — sections the change set invalidated.

**Code comments — concise and true.** A comment earns its place only by stating a constraint or intent the code can't show. Remove:
- narration ("increment the counter", "call the helper"),
- change-log commentary ("was previously X", "refactored from Y", "new in this PR"),
- **references to capataz issues, PR numbers, or review findings** ("fixes issue 07", "per PR #12", "addresses review feedback") — provenance belongs in commit messages and the backlog, not in source,
- comments the change set made false.

Rewrite verbose-but-load-bearing comments down to the constraint they protect. When in doubt whether a comment is load-bearing, read the code it guards before touching it.

## 3. Large README / CLAUDE.md changes need the user's OK

Small, targeted fixes — a drifted command, a wrong path, a stale sentence, a tightened line — apply directly. But **before** any large change to a README or CLAUDE.md (rewriting or deleting a section, restructuring the document, changing its overall shape), stop and ask the user: summarize what you'd change and why, and wait for approval. If you're running hands-off (as a subagent or in a dispatch with no user to ask), don't make the large change — file the proposal as an issue instead (see step 4) and move on.

Code-comment edits never need approval; they're always in scope.

## 4. Ambiguity goes to the backlog, not into edits

When a doc contradicts the code and you can't tell which side is wrong — the doc may be describing intended behavior the code fails to deliver — don't guess. File it:

```sh
capataz issue new <feature> "docs/code disagree: <where>" \
  --body "Doc says X (<file:line>), code does Y (<file:line>). Decide which is right; fix the loser."
```

Use the feature slug you were given; if none, use `docs-rot`. File large README/CLAUDE.md proposals the same way (step 3) when there's no user to ask.

## 5. Report

Print a short summary: per file, what you cut, tightened, or corrected (one line each is fine), plus any issues you filed. If the docs were already clean, say so and change nothing — a no-op pass is a valid result.
