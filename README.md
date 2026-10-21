<p align="center">
  <img src="docs/logo.svg" alt="capataz" width="600">
</p>

<p align="center">
  An opinionated workflow for shipping software with <a href="https://claude.com/claude-code">Claude&nbsp;Code</a> agents in <a href="https://herdr.dev">herdr</a>.<br>
  Plan a feature, split it into issues, drain the backlog with parallel agents in isolated worktrees,<br>
  and review it with fresh eyes.
</p>

*Capataz* is Spanish for **foreman**: it runs the crew so you can run the job.

## Why

Running one Claude Code agent is easy. Running several is where it falls apart:

- Two agents in the **same checkout** silently clobber each other's edits, with no conflict markers to warn you.
- An agent that **reviews its own work** grades its own homework — it inherits every assumption it made while writing the code.
- The work itself lives nowhere: no backlog, no statuses, no record of what was asked and what shipped.

[herdr](https://herdr.dev) runs the agents: panes, worktrees, agent state, persistence. capataz is the workflow on top. It gives agents a **file-based backlog** to work from, drains it with **parallel agents in isolated git worktrees** so concurrent work can't collide, and routes finished work through **fresh-context reviews** whose findings become new issues.

This repo is both the `capataz` CLI and the herdr plugin that puts the backlog and usage gauges into herdr popups.

## The opinions

1. **Work is split into vertical slices** small enough for one agent to finish in one sitting, each with a predicted file footprint (`Touches:`).
2. **Parallel agents never share a working tree.** Overlapping issues run in separate waves; concurrent ones run in separate git worktrees on separate branches.
3. **Implementers never review their own work.** Reviews run in fresh sessions with no memory of how the code was written, and their findings become new backlog issues.
4. **Files are the source of truth.** The backlog is plain markdown in your repo — no external tracker, no database, `grep`-able.

## The workflow

Everything runs through a pipeline of **skills** (installed into `~/.claude/skills`, so any Claude Code session can invoke them) and a **markdown backlog** in your repo:

```
 idea ──▶ plan.md ──▶ issues ──▶ ready-for-agent ──▶ parallel agents ──▶ merged
      /capataz-start   /capataz-splitter  /capataz-triage   capataz drain / /capataz-work
                                                                │
                            findings filed as new issues ◀── /capataz-review, /capataz-security
                            (back through triage — loop until a review comes back clean)
```

### The backlog

Issues are plain markdown, one feature per directory:

```
<repo>/.scratch/<feature>/issues/NN-slug.md
```

The backlog is local-only. `capataz install` adds `.scratch/` to your global git excludes file (`core.excludesFile`, default `~/.config/git/ignore`), so it is never committed or pushed, and no repo needs a `.gitignore` entry for it.

```markdown
# Add the login form

Status: ready-for-agent
Type: task
Blocked by: None
Touches: internal/auth/, main.go

## What to build
…
```

The `Status:` line is the kanban column. The lifecycle:

```
needs-triage ──▶ ready-for-agent ──▶ claimed ──▶ resolved
     │                (todo)          (doing)      (done)
     └──▶ wontfix                       ▲ │
                                 answer │ ▼ ask
                                      asking
```

`capataz issues` (or the herdr backlog popup) shows a repo's backlog grouped by feature, with status badges, attention-first counts, and the current parallel-safe wave. It re-reads the files live, so claims and resolutions show up as agents make them. The format is compatible with mattpocock's `/to-issues` / `/triage` / `/wayfinder`, so those planning skills interoperate.

### The skills

`capataz install` ships eleven skills:

**Plan** —
- **`capataz-start`** — the front door: grills a fuzzy idea into a lean plan (`.scratch/<feature>/plan.md`), then hands off to the splitter.
- **`capataz-splitter`** — breaks the plan into tracer-bullet vertical slices and publishes them as issues, exploring the codebase to predict each issue's `Touches:` so parallel-safe waves can be derived.

**Intake** —
- **`capataz-triage`** — walks `needs-triage`/`needs-info` issues, grills you for missing acceptance criteria and scope, then promotes each to `ready-for-agent` or `wontfix`. Review findings land here.
- **`capataz-investigate`** — diagnoses a bug to its root cause and files a `ready-for-agent` fix issue. It diagnoses; it does not fix.

**Build** —
- **`capataz-work`** — works one issue end to end: claim → implement → resolve.
- **`capataz-fix-conflict`** — resolves a conflicted merge-back so a drain never stalls on a `Touches:` misprediction.

**Review** —
- **`capataz-review`** — fresh-context correctness review of a feature's diff; files findings as issues.
- **`capataz-security`** — fresh-context, OWASP-lens audit of the same diff; files vulnerabilities as severity-tagged issues.
- **`capataz-comment-rot`** — pre-PR docs hygiene: scrubs the README, CLAUDE.md, and code comments touched by the changes.
- **`capataz-tfreview`** — fresh-context review of a branch's Terraform changes for races a script can't see (apply ordering, eventual consistency, shared resources). On demand only; neither the drain nor `capataz-work` runs it.

**Learn** —
- **`capataz-teach`** — explains a codebase area grounded in the real files (`file:line` citations), then quizzes you on it.

### Working one issue

In any Claude session in the repo, run `/capataz-work` on an issue file. It claims the issue, implements it, and resolves it. Ask it for a sanity check and it spawns a fresh-context subagent to check the change against the acceptance criteria before resolving.

### The drain

For a whole feature, drain the backlog from a shell in the repo:

```sh
capataz drain --repo "$PWD" <feature>                  # up to 3 issues at once
capataz drain --repo "$PWD" --concurrency 2 <feature>
capataz drain --dry-run --repo "$PWD" <feature>        # show the plan, touch nothing
```

From each issue's `Touches:` line, capataz derives which ready issues are **parallel-safe** (pairwise-disjoint files, no blocking edge). The drain then loops:

1. Take a parallel-safe wave of `ready-for-agent` issues, up to `--concurrency`.
2. For each, create a herdr worktree on branch `<feature>-NN-slug` (prefix it with `--branch-prefix` or `drain.branch_prefix`), pre-trust it (`capataz trust-dir`), and start a fresh Claude agent running `capataz-work` in it.
3. Wait for each agent, check it actually committed, and merge its branch back into your base. A conflicting merge starts a `capataz-fix-conflict` agent in the base repo; the drain checks the merge completed before moving on.
4. Re-scan — issues become eligible as their blockers resolve — and repeat until nothing is left.

Each agent is its own herdr pane, so you can watch or step into any of them.

**Questions.** A drain worker that needs a decision from you doesn't ask in chat — nobody reads the drain's chat. It runs `capataz issue ask <issue> "<question>"`, which records the question under the issue's `## Comments` and sets `Status: asking`, then ends its turn. The drain shows a herdr notification with the question and logs the exact command to answer it:

```sh
capataz issue answer <issue> "cookies"
```

(or tell any Claude session to). The drain picks the answer up within seconds and prompts it into the **same** worker session, which continues where it stopped; a worker may ask more than once. If you move the issue somewhere else instead (`needs-info`, `wontfix`, …), the drain stops waiting and keeps the worktree.

**Stalls.** The drain never silently abandons a worker:

- **Blocked** — an agent waiting on a prompt (usually a permission prompt) shows as `blocked` in herdr. The drain notifies once and waits for it to resume, then verifies and merges as usual.
- **No progress** — an agent herdr reports as working whose worktree has had no file change for `drain.stall_minutes` (default 15) gets one notification naming the pane and worktree. A later change re-arms it. The drain never nudges or kills it.
- **Timeout** — an agent still running after `--timeout` (default 1h) is notified and left in place, unmerged. Time spent blocked or waiting on an answer doesn't count against it.
- **Stopped without committing** — an agent that ends its turn with its issue still `claimed` and nothing committed (or uncommitted changes) gets `--resume-grace` (default 3 minutes, out of `--timeout`) to start working again, since Claude Code wakes an agent whose background shell finishes. If it resumes, the drain waits on it as usual. Otherwise it is never cleaned up while its worktree has changes or its issue is still `claimed` (it may have asked in chat anyway — you get a notification for that case). The worktree and branch stay for you.

> **⚠️ Parallelism belongs to the drain.** Sessions you start by hand run in the repo's own working tree — two of those on one repo at once can clobber each other. To parallelize, let the drain isolate each agent in its own worktree.

> **Permissions.** Dispatched agents run in Claude Code's default permission mode with `Bash`, `Edit`, `Write`, `Read`, `Glob` and `Grep` pre-allowed; any other permission prompt shows the agent as `blocked` in herdr (see Stalls). `--skip-perms` runs them with `--dangerously-skip-permissions` instead (accept the one-time bypass screen first). A per-project allowlist in your repo's `.claude/settings.json` is a safer way to cut the prompt noise.

### The review loop

When a feature's issues are resolved, open a **fresh** Claude session and run `/capataz-review` (and `/capataz-security` when the change touches auth, input parsing, file/exec, network, or secrets). A fresh session has no memory of how the code was written, so it judges soundness instead of trusting the implementer. Findings are filed as `ready-for-agent` issues; triage them and drain again until a review comes back clean.

### The backlog CLI

The skills call these; you can too:

```sh
capataz issue list .                                # this repo's backlog
capataz issue new  <feature> "<title>" --body "…"   # file a new issue
capataz issue claim   .scratch/f/issues/01-x.md
capataz issue resolve .scratch/f/issues/01-x.md --answer "what shipped"
capataz issue comment .scratch/f/issues/01-x.md "a note under ## Comments"
capataz issue ask     .scratch/f/issues/01-x.md "a question"   # Status: asking
capataz issue answer  .scratch/f/issues/01-x.md "the answer"   # back to claimed; prints the answer
capataz issue waves <feature> --json                # the parallel-safe dispatch plan
```

## Requirements

- **[herdr](https://herdr.dev) ≥ 0.8.0**, with its Claude integration installed: `herdr integration install claude`. The drain waits on herdr's agent state, so **without the integration the drain hangs**.
- **Claude Code**
- **Go 1.26+** to build
- **macOS** for the plan-usage gauge (it reads the Keychain). Everything else also runs on Linux.

## Install

```sh
git clone https://github.com/bmena34/capataz && cd capataz
make install        # builds and copies capataz to /usr/local/bin (prompts for sudo)
capataz install     # skills + agents into ~/.claude, default config, global .scratch ignore
```

The skills call `capataz issue …`, so the CLI must be on your PATH.

Then link the plugin and build its binary:

```sh
herdr plugin link .        # from the repo root
sh scripts/build.sh        # builds ./bin/capataz, which the plugin runs
herdr server reload-config
herdr plugin list          # should show bmena34.capataz enabled
```

`herdr plugin install` runs the build step itself; after `herdr plugin link`, run it by hand (and again after pulling changes).

### Keybinds

Add to `~/.config/herdr/config.toml`, then `herdr server reload-config`:

```toml
# ctrl+b i — backlog popup for the focused pane's repo
[[keys.command]]
key = "prefix+i"
type = "plugin_action"
command = "bmena34.capataz.issues"
description = "capataz: open backlog"

# ctrl+b u — plan usage + per-agent context popup
[[keys.command]]
key = "prefix+u"
type = "plugin_action"
command = "bmena34.capataz.usage"
description = "capataz: usage & context"
```

Or invoke them directly: `herdr plugin action invoke issues` / `herdr plugin action invoke usage`.

## The herdr plugin

- **Backlog popup** (`issues`) — `capataz issues` in a popup at ~70% of the terminal, scoped to the focused pane's repo. `j`/`k` to move, `r` to reload, `q` to close.
- **Usage & context popup** (`usage`) — `capataz usage-view`, refreshing every 2s until `q`:

  ```
  Plan: Enterprise / API (billed per token — no rate-limit bars)

  Context
  ▸ usage popup polish   51k / 1.0M   [█░░░░░░░░░]   5%
    drain-03-usage-pane  148k / 200k  [███████▌░░]  74%
  ```

  `▸` marks the pane you opened it from. Each agent's context is read straight from its Claude Code transcript, located via the session id in `herdr agent list`. Rows are labeled with the pane's terminal title, falling back to the repo directory name. Panes running another agent show as `no context data` rather than disappearing.

`capataz usage-view` also runs outside herdr: `-once` prints one frame, `-no-plan` skips the plan gauge, `-limit 1m` overrides the assumed context window.

## Configuration

Optional, at `~/.config/capataz/config.json` (created by `capataz install`; `capataz config` prints it):

```json
{
  "usage": {
    "enabled": true,
    "url": "https://api.anthropic.com/api/oauth/usage",
    "keychain_service": "Claude Code-credentials",
    "plan_type": ""
  },
  "context": { "limits": {} },
  "drain": { "branch_prefix": "", "stall_minutes": 15 }
}
```

- `context.limits` — pin a context-window size per model id. Transcripts record a bare model id with no `[1m]` marker, so a model offered at both 200k and 1M looks like 200k unless you pin it: `{ "claude-opus-5": "1m" }`. `CAPATAZ_CONTEXT_LIMIT=1m` overrides every session for one run.
- `drain.branch_prefix` — prepended to dispatch branch names; `--branch-prefix` overrides it per run.
- `drain.stall_minutes` — minutes a working agent may go without a file change before the drain notifies you.
- `drain.repos` — per-repo overrides of the `drain` keys, keyed by absolute repo path: `{ "/Users/me/code/app": { "stall_minutes": 30 } }`.
- `usage.plan_type` — `"api"` replaces the session/weekly bars with a "billed per token" label for enterprise or API-billing plans. Leave blank to auto-detect.

> **Note on the usage gauge.** To show your plan usage, `capataz` reads your Claude Code OAuth token from the macOS Keychain and calls `api/oauth/usage` — the **same endpoint Claude Code's `/usage` uses**. It is *unofficial* and undocumented, and may change or stop working at any time. It only reads *your* token to display *your* usage. Set `usage.enabled` to `false` to turn it off, or update `url` / `keychain_service` if Anthropic changes them.

## Commands

```
capataz issues [dir]          backlog viewer for the repo at dir (default cwd)
capataz issue …               claim/resolve/status/comment/ask/answer/list/new/waves (used by skills)
capataz drain [flags] <f>     drain feature f's backlog with parallel herdr agents
capataz usage                 print the plan-usage gauge
capataz usage-view [flags]    live plan-usage + per-agent context view
capataz trust-dir <path>      pre-seed Claude folder-trust for path
capataz config                print the config file path + contents
capataz install               install skills + agents, default config, global .scratch ignore
capataz uninstall             remove the installed skills + agents
capataz version               print version
```

## Upgrading from the tmux version

capataz used to be a tmux dashboard with its own hooks and launchd daemon; the last version of that is tagged `tmux-final`. Running `capataz install` (or `uninstall`) from this version removes the old capataz hooks from `~/.claude/settings.json` (backing it up to `settings.json.bak`) and stops and deletes the old launchd daemon.

## Uninstall

```sh
capataz uninstall                  # removes the skills + agents
herdr plugin unlink bmena34.capataz
make uninstall                     # removes /usr/local/bin/capataz
```

The config and the global `.scratch/` ignore stay, so an existing backlog is never exposed to a commit.

## License

[MIT](LICENSE)
