---
name: capataz-drain
description: Hands-off orchestrator that drains a feature's .scratch backlog — claim each ready issue, work it via a capataz-work subagent in its own git worktree, merge the branch back, and re-scan until nothing is left, resolving conflicts automatically. Invoked when capataz dispatches a drain session (the dashboard's p key). Also use when the user says "drain the backlog", "hands-off run this feature", or "work the whole <feature> backlog".
---

# capataz-drain

You are the **orchestrator** of a hands-off backlog drain. capataz dispatched you (one session) to work a whole feature's `.scratch` backlog to completion without further input — instead of the operator babysitting one session per issue. You don't implement issues yourself: you **delegate each one to a `capataz-work` subagent** and keep the loop turning until the backlog is drained. Keeping the work in subagents is deliberate — their implementation detail (file reads, diffs, test output) stays in the subagent and never bloats your context, so you can drive a large backlog from one session.

The dispatch prompt gives you:

> Use the /capataz-drain skill to hands-off drain the `<feature>` feature's .scratch backlog, merging into this repo's current branch (the base). Start with these ready issues: `<NN, NN, …>`. Then keep draining any sibling issues in the same feature that become ready as their blockers resolve, until none are left. …

So you have: the **feature slug**, the **base branch** (this repo's current branch — you're in the primary checkout), a **seed set** of issue numbers, and optionally a **max-concurrency** cap (default to **3** if none is given).

**This is fully autonomous.** Do not stop for confirmation between waves, before a merge, or after a conflict. The only reasons to stop are: your scope is drained, or something is genuinely stuck and needs a human (record it and move on — don't wedge the loop).

## Two modes — whole-feature vs. bounded

The dispatch prompt's phrasing selects one of two modes. They share **all** the per-issue mechanics (parallel-safe waves, worktree per issue, `capataz-work` subagent, merge-back, `capataz-fix-conflict` on conflict) — they differ **only** in what defines "done" and whether you re-scan for newly-ready siblings:

- **Whole-feature mode (default).** The prompt says to **drain the whole feature** — e.g. "hands-off drain the `<feature>` backlog … keep draining any sibling issues that become ready as their blockers resolve, until none are left." You run the full loop below: scan → wave → merge → **re-scan** until a scan finds nothing ready. This is the historical behavior and is unchanged.

- **Bounded mode.** The prompt enumerates an **exact, exhaustive** issue set and tells you to work only that set — e.g. "work exactly issues `NN, NN`; do not pull in newly-ready siblings; stop when these are resolved." You run the same waves over **only** the enumerated issues, and **stop when every issue in that set is `resolved`** (or genuinely stuck). You **do not re-scan** the feature for newly-ready siblings, and you never pick up an issue that isn't in the enumerated set — even if its blockers resolve and it becomes ready mid-drain.

If the prompt gives a seed set but says to keep draining siblings until none remain, that's **whole-feature** (the seed is just a starting point). Only treat it as **bounded** when the prompt is explicit that the enumerated set is exhaustive and forbids pulling in newly-ready siblings ("do not pull in newly-ready siblings" / "work exactly these" / "stop when these are resolved"). When in doubt, prefer whole-feature. The TUI's `drainPrompt` emits the matching phrasing for whichever mode the operator selected.

## The backlog format (recap)

Issues live at `.scratch/<feature>/issues/NN-<slug>.md` with header lines `Status:`, `Type:`, `Blocked by: NN, NN`, and `Touches: <paths>`. An issue is **ready** when `Status: ready-for-agent` and every `Blocked by` issue is `resolved`. Two issues are **parallel-safe** when their `Touches` paths are pairwise disjoint (a path equals or is a path-segment prefix of another ⇒ they overlap). Files are the source of truth — you re-read them each pass.

## Process — the loop

Repeat until you have no more ready issues **in scope** (whole feature, or the enumerated set in bounded mode):

### 1. Scan for the ready wave

Read every issue file under `.scratch/<feature>/issues/`. Select the ones that are `ready-for-agent` with all `Blocked by` resolved. **In bounded mode, first restrict to the enumerated issue set** — ignore any file whose number isn't in the set, even if it's ready. From those, pick a **parallel-safe wave**: greedily take ready issues whose `Touches` are pairwise disjoint, up to the **max-concurrency** cap. Issues that overlap the ones you picked simply wait for a later wave — that's how overlaps serialize safely.

If nothing in scope is ready but in-scope issues remain `claimed`/blocked, you're done (see step 5).

### 2. Dispatch the wave — one capataz-work subagent per issue, each in its own worktree

For each issue in the wave, create an isolated worktree on its branch, then hand it to a `capataz-work` subagent:

```sh
# branch = capataz/<feature>-<NN>-<slug>  (must match this exact naming)
git worktree add ~/.capataz/worktrees/drain-<feature>/<NN>-<slug> -b capataz/<feature>-<NN>-<slug>
```

Then spawn a **subagent** with the Agent tool (one per issue — they run concurrently) whose prompt tells it:

- Use the `/capataz-work` skill on the issue at its **absolute path** (`/…/.scratch/<feature>/issues/NN-<slug>.md`). The `.scratch` backlog is gitignored and absent inside the worktree, so it must use the absolute path in the primary checkout for `capataz issue claim/resolve`.
- It is in **isolated-worktree mode**: `cd` into the worktree dir you created, do all work there, and **commit on the branch** when it resolves (`git add -A && git commit -m "<feature>/NN: <what>"`) — do **not** merge or switch branches.
- Claim before implementing, run the repo's build/tests, resolve when the acceptance criteria are met.

No folder-trust or permission pre-seeding is needed: a subagent runs inside **your** session, so it inherits your permissions — there's no fresh `claude` process to stall on a trust or permission prompt. (This is the whole reason the drain uses subagents instead of one dispatched session per issue.)

### 3. Collect results, then merge each resolved branch back

Wait for the wave's subagents to finish. For each issue, check its `Status`:

- **`resolved`** → merge its branch into the base, in the primary checkout, without switching branches:

  ```sh
  git merge --no-ff capataz/<feature>-<NN>-<slug>
  ```

  - **Clean merge** → it lands. Clean up: `git worktree remove ~/.capataz/worktrees/drain-<feature>/<NN>-<slug>` and `git branch -d capataz/<feature>-<NN>-<slug>`.
  - **Conflict** → do **not** resolve it yourself. Spawn a **`capataz-fix-conflict` subagent** and tell it a merge of `capataz/<feature>-<NN>-<slug>` into the base is in progress and conflicted; it resolves, builds/tests, and commits the merge. Then continue — do not pause. Once it's done, remove the worktree + branch as above.

- **`needs-info` / `claimed`-but-not-resolved / blocked report** → the subagent hit something underspecified or got stuck. **Skip it**: leave its status, note it for the summary, remove its (uncommitted) worktree if you created one, and keep draining the rest. Never let one stuck issue wedge the loop.

Merge one branch at a time so conflicts are attributable.

### 4. Re-scan (whole-feature mode) / next wave (bounded mode)

**Whole-feature mode:** go back to step 1 and re-scan the whole feature. Issues whose blockers you just resolved are now ready and will be picked up in the next wave. The loop naturally walks the dependency graph wave by wave until nothing is left.

**Bounded mode:** go back to step 1 but keep the scope pinned to the enumerated set — you're only draining the next parallel-safe wave *of that set*. Do **not** pull in newly-ready siblings outside the set, even if resolving an in-set issue unblocked them. Stop once every enumerated issue is `resolved` (or stuck — see step 5). The only difference from whole-feature mode is this scope pin; the mechanics of each wave are identical.

### 5. Report

When you have no more ready issues in scope, stop and print a summary:

- Which issues **drained** (worked + merged), in order.
- Any **conflicts** hit and that a `capataz-fix-conflict` subagent resolved them.
- Anything left **needs-info / blocked / skipped**, and why — these need a human.
- In bounded mode, state that the enumerated set is complete (and, if any in-set issue is stuck, which). Do not report on out-of-set siblings — they're out of scope by design.

## Resumability

If you're re-launched on a backlog that's partly done, just start the loop: `resolved` issues are skipped by the scan, and `capataz issue claim` is a compare-and-set (it succeeds only from `ready-for-agent`), so you can't double-claim or double-work. The files are the state; you hold none of your own.

## Guardrails

- **Don't implement issues in your own thread** — always delegate to a `capataz-work` subagent, so your context stays thin and the drain scales.
- **Don't nest merges or start unrelated ones** — you merge only this feature's branches, one at a time.
- **Don't stop for confirmation.** Autonomous means autonomous. Stop only when your scope is drained or when reporting genuinely stuck issues.
- **In bounded mode, respect the bound.** Never work an issue that isn't in the enumerated set, and never re-scan for newly-ready siblings. The set is exhaustive by contract; pulling in extras defeats the operator's selection.
