---
name: capataz-splitter
description: Break a plan, PRD, or loose idea into tracer-bullet vertical slices and publish them as .scratch backlog issues with Blocked-by edges and a Touches file-scope line — the capataz-native replacement for mattpocock's /to-issues. Adds Touches so capataz can derive parallel-safe dispatch waves. Invoked when the user says "split this into issues", "turn this plan into a backlog", "to-issues this", or hands you a spec to break down.
---

# capataz-splitter

Take a plan / PRD / loose idea and turn it into a set of independently-workable issues in this repo's `.scratch/` backlog. This is the capataz-native replacement for mattpocock's `/to-issues`: same tracer-bullet vertical-slice discipline, same on-disk format, **plus** a `Touches:` line per issue that predicts which files/dirs the slice will modify. capataz uses `Touches` + `Blocked by` to derive **parallel-safe waves** — which issues can be dispatched to concurrent agents without colliding — so getting `Touches` right is what makes safe parallel work possible.

**Where these issues go.** Once published, the operator dispatches them from the capataz dashboard — one at a time, or a parallel-safe wave with `p` — each to a fresh `capataz-work` agent in its own git worktree; a `capataz-review` pass later files follow-up issues. So write every slice **self-contained and agent-ready** (a fresh agent with no memory of this session will implement it), and get `Touches` right — that's what lets a wave run in parallel without collisions. (Run `capataz issue` with no args for the full CLI.)

## What "good" looks like

Every issue is a **tracer-bullet vertical slice**: a thin, end-to-end path through *all* the layers it needs (data → logic → wire → UI, or whatever the stack is), demoable on its own. **Not** a horizontal layer ("add all the DB tables", "build the whole UI"). If a slice can't be demoed or tested by itself, it's too horizontal — reshape it.

Beyond `/to-issues`, you also shape the slices to **minimize cross-issue file overlap**, because two issues that touch disjoint files can run in parallel. When two slices would both edit the same central file, either redraw the boundary so they don't, or accept it and let those two serialize (via `Blocked by` or just shared `Touches`).

## Process

### 1. Read the input and explore the codebase

The plan comes from the conversation context or an argument. Read it fully, then **explore the repo** so your slices and their `Touches` lines are grounded in reality:

- Find the layers the work spans (entry points, packages/dirs, wire types, config, UI) — `git ls-files`, `rg`, read the CLAUDE.md / READMEs / ADRs in the areas you'll touch.
- For each candidate slice, predict the concrete files and directories it will create or modify. When unsure whether a file is in scope, **include it** — see the conservatism rule below.
- Note the **central / shared** files that many slices seem to want (a proto/wire-types file, a router, a central config, a shared model). These are the serialization hotspots.

### 2. Draft the slices

Draft 3–8 vertical slices (right-size to the plan). For each, write down:

- A concise **title** (imperative, e.g. "Persist sessions to SQLite").
- **Acceptance criteria** — the demoable end state, as checkboxes.
- **Blocked by** — which sibling slices must land first (real ordering only: a slice that consumes another's API/schema; don't invent ordering to dodge overlap).
- **Touches** — the files/dirs it will modify (see format below).

**Draw the boundaries to minimize overlap.** Prefer slices that localize their changes to their own files/packages. If two slices both need a central file, ask whether the boundary can move so only one owns it; if not, keep them both listing that file (they'll serialize).

### 3. The Touches line — format and semantics

`Touches:` is a header line that sits near the top of the issue file, in the same block as `Status:` / `Type:` / `Blocked by:`. It is a comma-separated list of **repo-relative paths** — files and/or directories:

    Touches: internal/issues/issues.go, internal/tui/

**Overlap semantics (this is exactly how capataz compares them):** two issues **overlap** if any path from one **equals**, or is a **path-segment prefix of**, any path from the other. So `internal/tui` prefixes `internal/tui/issues.go` (overlap), but `internal/t` does **not** prefix `internal/tui` (segment-wise, not string-wise). Overlapping issues are **not** parallel-safe and must run in different waves.

**Be conservative — over-list when unsure.** Under-listing is the dangerous error: a file you forgot makes capataz think two issues are parallel-safe when they actually collide, so two agents stomp each other. Over-listing at worst forces two non-colliding issues to serialize — slower, but safe. When in doubt, list the file, or list the enclosing directory.

- If a slice edits a **central/shared** file, list that exact file so every slice touching it overlaps and they serialize. Call it out in the issue body too, so a human reviewing the waves knows why those issues can't parallelize.
- An issue with **no** `Touches` line has unknown scope and capataz treats it as overlapping **everything** (never parallel-safe). So always add one — even `Touches: <best-guess dir>/` beats omitting it.

### 4. Quiz the user before publishing

Do **not** publish yet. Present the proposed breakdown and get sign-off, like `/to-issues`. Show, for each slice: title, one-line scope, `Blocked by`, and `Touches`. Then ask the user to confirm or adjust:

- **Granularity** — are slices the right size, each a demoable vertical path? Any that are secretly horizontal?
- **Dependencies** — are the `Blocked by` edges right, and is the dependency graph free of cycles?
- **Overlap / parallelism** — call out which slices will run in parallel and which serialize (and on which shared file). Confirm the overlaps are real, not from over-listing you could tighten — and that no overlap is missing.

Iterate until the user is happy. Only then publish.

### 5. Publish each issue

The `capataz issue new` template writes `Status: ready-for-agent`, `Type: task`, and `Blocked by: None`, with **no** `Touches:` line — so create the file, then edit it to add `Touches:` and fix `Blocked by:`.

For each slice, in dependency order (so numbers line up with `Blocked by` references):

```sh
capataz issue new <feature> "<title>" --body "What to build (the vertical slice), and why. Note any central/shared file this must touch and therefore serialize on."
```

This prints the new file path (e.g. `.scratch/<feature>/issues/03-<slug>.md`). Then **edit that file** to:

1. Add a `Touches:` line in the header block, right after `Blocked by:` — comma-separated repo-relative paths, conservative.
2. Replace `Blocked by: None` with the real blocker numbers (e.g. `Blocked by: 01, 02`), if any. Reference issues by their `NN` number within the same feature.
3. Fill in the `## Acceptance criteria` checkboxes with the demoable end state.

The header block should end up looking like:

    # Persist sessions to SQLite

    Status: ready-for-agent
    Type: task
    Blocked by: 01
    Touches: internal/store/, internal/daemon/daemon.go

Leave `Status: ready-for-agent` — that's what marks an issue dispatchable.

### 6. Report

Print a short summary: the feature slug, the issues you created (numbers + titles), and the **parallel waves** capataz will derive — group the issues by which can run concurrently (disjoint `Touches` and blockers satisfied) vs. which serialize, and name the shared file(s) forcing any serialization. This is the payoff: accurate `Touches` + `Blocked by` is exactly what lets capataz dispatch a wave of agents in parallel without collisions.
