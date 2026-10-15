---
name: capataz-review
description: Do an in-depth, fresh-context code review of the current changes and file every finding back into the local .scratch backlog as a ready-for-agent issue. Run it in a fresh session after a feature's issues are all resolved. Also use when the user says "review the changes and file issues" or "fresh review of <feature>".
---

# capataz-review

You are a **fresh review context**, deliberately started with no memory of how this code was written — so you can judge whether it's actually sound rather than trusting the implementer's account. Review the current changes and turn every real finding into a new issue in the `.scratch` backlog, so the work loops: review → issues → fix → review, until a review comes back clean.

You were given a **feature slug** (the `.scratch/<feature>/` the findings should be filed under). File findings there.

## 1. Gather the change set

Issues don't record which files they touched, so review the whole current change set, not a per-issue slice:

```sh
git diff @{upstream}...HEAD   # committed work on this branch (fallback: git diff main...HEAD)
git diff HEAD                 # uncommitted working-tree changes
```

Read the diffs, and read the surrounding code for each hunk — a bug in an unchanged line of a touched function is in scope.

## 2. Review in depth

Judge correctness **and** architecture soundness. Because you're a fresh context, don't take comments or commit messages as truth — verify against the code. For breadth, you may spawn subagents on different angles (correctness / removed-behavior / cross-file / conventions) and collect their findings. For each candidate finding, confirm it against the actual code before filing — no speculation, no hallucinated line numbers. Consult any CLAUDE.md / ADRs that govern the changed code.

Do **not** fix anything. Your output is issues, not edits.

## 3. File findings as issues

For each confirmed finding, file a ready-for-agent issue into the feature's backlog:

```sh
capataz issue new <feature> "<concise title>" \
  --body "What's wrong, where (the failure scenario), and what 'fixed' looks like."
```

Keep each issue a single, independently-fixable slice. Rank by severity in the titles if helpful. If you find nothing real, file nothing and say so.

## 4. Report

Print a short summary: how many issues you filed (with their new numbers/titles) and your overall read on whether the code and architecture are sound. The new issues show as `todo` in the backlog (`capataz issues`) for the next round.
