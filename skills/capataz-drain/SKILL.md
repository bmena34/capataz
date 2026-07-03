---
name: capataz-drain
description: Hands-off orchestrator that drains a feature's .scratch backlog — claim each ready issue, work it via a capataz-work subagent in its own git worktree, merge the branch back, and re-scan until nothing is left, resolving conflicts automatically. Invoked when capataz dispatches a drain session (the dashboard's p key). Also use when the user says "drain the backlog", "hands-off run this feature", or "work the whole <feature> backlog".
---

# capataz-drain

You are the **orchestrator** of a hands-off backlog drain. capataz dispatched you (one session) to work a whole feature's `.scratch` backlog to completion without further input — instead of the operator babysitting one session per issue. You don't implement issues yourself: you **delegate each one to a `capataz-work` subagent** and keep the loop turning until the backlog is drained. Keeping the work in subagents is deliberate — their implementation detail (file reads, diffs, test output) stays in the subagent and never bloats your context, so you can drive a large backlog from one session.

The dispatch prompt gives you:

> Use the /capataz-drain skill to hands-off drain the `<feature>` feature's .scratch backlog, merging into this repo's current branch (the base). Start with these ready issues: `<NN, NN, …>`. Then keep draining any sibling issues in the same feature that become ready as their blockers resolve, until none are left. …

So you have: the **feature slug**, the **base branch** (this repo's current branch — you're in the primary checkout), a **seed set** of issue numbers, and optionally a **max-concurrency** cap (default to **3** if none is given).

**This is fully autonomous.** Do not stop for confirmation between waves, before a merge, or after a conflict. The only reasons to stop are: the backlog is drained, or something is genuinely stuck and needs a human (record it and move on — don't wedge the loop).

## The backlog format (recap)

Issues live at `.scratch/<feature>/issues/NN-<slug>.md` with header lines `Status:`, `Type:`, `Blocked by: NN, NN`, and `Touches: <paths>`. An issue is **ready** when `Status: ready-for-agent` and every `Blocked by` issue is `resolved`. Two issues are **parallel-safe** when their `Touches` paths are pairwise disjoint (a path equals or is a path-segment prefix of another ⇒ they overlap). Files are the source of truth — you re-read them each pass.

## Process — the loop

Repeat until a scan finds no ready issues:

### 1. Scan for the ready wave

Read every issue file under `.scratch/<feature>/issues/`. Select the ones that are `ready-for-agent` with all `Blocked by` resolved. From those, pick a **parallel-safe wave**: greedily take ready issues whose `Touches` are pairwise disjoint, up to the **max-concurrency** cap. Issues that overlap the ones you picked simply wait for a later wave — that's how overlaps serialize safely.

If nothing is ready but issues remain `claimed`/blocked, you're done (see step 5).

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

### 4. Re-scan

Go back to step 1. Issues whose blockers you just resolved are now ready and will be picked up in the next wave. The loop naturally walks the dependency graph wave by wave.

### 5. Report

When a scan finds nothing ready, stop and print a summary:

- Which issues **drained** (worked + merged), in order.
- Any **conflicts** hit and that a `capataz-fix-conflict` subagent resolved them.
- Anything left **needs-info / blocked / skipped**, and why — these need a human.

## Resumability

If you're re-launched on a backlog that's partly done, just start the loop: `resolved` issues are skipped by the scan, and `capataz issue claim` is a compare-and-set (it succeeds only from `ready-for-agent`), so you can't double-claim or double-work. The files are the state; you hold none of your own.

## Guardrails

- **Don't implement issues in your own thread** — always delegate to a `capataz-work` subagent, so your context stays thin and the drain scales.
- **Don't nest merges or start unrelated ones** — you merge only this feature's branches, one at a time.
- **Don't stop for confirmation.** Autonomous means autonomous. Stop only when drained or when reporting genuinely stuck issues.
