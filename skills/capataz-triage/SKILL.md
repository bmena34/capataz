---
name: capataz-triage
description: Refine rough backlog issues into dispatchable ones — walk each needs-triage / needs-info issue in the local .scratch backlog, grill the user for missing acceptance criteria and scope, then move it to ready-for-agent (with a Touches: line) or wontfix. The intake step the review/security skills feed into. Invoked when the user says "triage the backlog", "triage <NN-slug>", or "is this issue ready?".
---

# capataz-triage

Turn rough, under-specified backlog issues into ones a fresh `capataz-work` agent can actually pick up. capataz's `Status` vocabulary has intake states — `needs-triage` (just filed, unreviewed) and `needs-info` (reviewed, but blocked on a human answer) — and `capataz-work` refuses to implement an issue in either, telling the user it "needs triage first". This skill is that step: for each rough issue you decide whether it's real, extract the missing spec from the user, and either promote it to `ready-for-agent` or close it as `wontfix`.

This complements `capataz-review` and `capataz-security`, which file raw findings straight into the backlog — some of those land under-specified and want a triage pass before an agent grabs them.

You work through the **existing CLI only** — `capataz issue status` moves an issue between states, `capataz issue comment` records the decision. No new subcommands. (Run `capataz issue` with no args for the full list.) The `Touches:` line has no CLI, so you add it by editing the file directly, exactly as `capataz-splitter` does.

## Process

### 1. Find what needs triage

Scope to the backlog you were given (a feature slug, a single `NN-slug`, or the whole repo). List it and pick out the intake-state issues:

```sh
capataz issue list .          # overview: numbers, titles, statuses
```

Target every issue whose `Status:` is `needs-triage` or `needs-info`. If you were handed one specific issue, just that one. Read each target in full — body, acceptance criteria, and any `## Comments` (a `needs-info` issue usually records *what* question is open).

If nothing is in an intake state, say so and stop — there's nothing to triage.

### 2. Assess each issue

For each target, read it against the bar a `capataz-work` agent needs to succeed with **no memory of this conversation**:

- **Is it real / in scope?** A duplicate, an already-fixed thing, or something the project won't do → headed for `wontfix`.
- **Is the goal clear?** What behavior should exist when it's done?
- **Are there acceptance criteria?** Concrete, checkable, demoable — not "make it better".
- **Is the scope known?** Which files/dirs the slice will touch (you need this for the `Touches:` line — see step 4).

If it already clears the bar, it just needs the `Touches:` line and promotion. If not, you need answers.

### 3. Grill the user for what's missing

Don't guess the spec — extract it. For each issue that's underspecified, ask the user the specific questions that unblock it: the missing acceptance criteria, the ambiguous scope, the open decision a `needs-info` issue is parked on. Batch your questions per issue so the user can answer in one pass. Keep going until you either have enough to write real acceptance criteria, or conclude the issue shouldn't be done.

While you gather answers, record them so they survive into the fresh work session:

```sh
capataz issue comment .scratch/<feature>/issues/NN-<slug>.md "Triage: <decision / answer / clarified scope>"
```

If a criterion or scope detail is worth having in the issue body itself (not just a comment), edit the body directly to sharpen it — the file is the source of truth a `capataz-work` agent reads.

### 4. Promote or close each issue

**Promote to ready-for-agent.** Once the issue has clear acceptance criteria and known scope:

1. Make sure the body carries the acceptance criteria and scope you extracted (edit it if the grilling turned up more).
2. Add a `Touches:` line to the header block — right after `Blocked by:`, a comma-separated list of repo-relative paths (files or dirs) the slice will modify. Be **conservative: over-list when unsure** — a forgotten file makes capataz think two issues are parallel-safe when they collide, so two agents stomp each other; over-listing at worst forces a needless serialize. Same semantics as `capataz-splitter`: two issues overlap if any path of one equals or is a path-segment prefix of any path of the other. If the issue already has a `Touches:` line, correct it rather than duplicating.
3. Flip the status:

   ```sh
   capataz issue status .scratch/<feature>/issues/NN-<slug>.md ready-for-agent
   ```

   The header should end up like:

       Status: ready-for-agent
       Type: task
       Blocked by: None
       Touches: internal/issues/, internal/tui/issues.go

   `ready-for-agent` is what marks it dispatchable and, with an accurate `Touches:`, parallel-safe.

**Close as wontfix.** If the issue is a duplicate, already done, or something the project won't pursue, record why and close it:

```sh
capataz issue comment .scratch/<feature>/issues/NN-<slug>.md "Triage: wontfix — <reason>."
capataz issue status  .scratch/<feature>/issues/NN-<slug>.md wontfix
```

**Still blocked?** If you couldn't get the answer needed (the user deferred, or a decision is pending elsewhere), leave it `needs-info` and note in a comment exactly what's outstanding, so the next triage pass resumes cleanly. Don't promote an issue you can't actually specify.

Only ever change `Status:` through `capataz issue …`, never by hand-editing the line — that's what keeps the backlog viewer and other agents consistent.

### 5. Report

Print a short summary: which issues you promoted to `ready-for-agent` (with the `Touches:` you assigned), which you closed `wontfix` (with the reason), and any you left `needs-info` (with what's still open). The promoted ones now show as dispatchable `todo` in the backlog, ready for `capataz-work` — one at a time, or in parallel waves via `capataz drain`.
