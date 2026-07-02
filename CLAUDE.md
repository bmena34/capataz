# CLAUDE.md

Guidance for working on **capataz** with Claude Code. User-facing docs are in `README.md`; this file is the contributor/agent map: architecture, the dev loop, and the non-obvious gotchas.

## What it is

A terminal dashboard that supervises Claude Code agents running in tmux. **macOS-only** (it uses the macOS Keychain via `security` and `launchd` for the daemon). Pure Go, single binary with subcommands.

## Build / test / run

```sh
make build                                  # build ./capataz (local dev binary)
make install                                # build + sudo install to /usr/local/bin
make vet                                    # go vet ./...
make test                                   # go test ./...
go test ./internal/tui -run TestRenderNoPanic   # render regression (every size 1×1..80×50)
```

The binary is multiplexed by subcommand (`main.go`): default → dashboard; `daemon`, `hook`, `install`, `uninstall`, `status`, `logs`, `quit`, `config`, `issue`, `popup`, `attach`, `keybind`, `statusline`.

**Dev loop — what to do after changing each component:**
- **daemon code** (`internal/daemon`): `go build -o capataz . && ./capataz restart`. The running daemon is a snapshot of the binary at launch.
- **dashboard code** (`internal/tui`): rebuild, then relaunch the dashboard. The **popup re-runs the binary fresh on every `prefix+g`**, so it picks up changes immediately; **sidebars** are long-lived and must be re-attached (`prefix+a`).
- **hook code** (`internal/hook`): rebuilt binary is re-exec'd on every hook call, so changes apply immediately. But **adding/removing a hook event** requires `./capataz install` (edits `~/.claude/settings.json`) **and** restarting any Claude sessions — hooks load at session start.

Debug: `./capataz status`, `./capataz logs` (app log at `~/.capataz/capataz.log`), `CAPATAZ_DEBUG=1` for verbose.

## Architecture

Data flows **hook → daemon → dashboard(s)**:

- **`internal/hook`** — `capataz hook <kind>` is invoked by Claude Code hooks. Reads the hook JSON from stdin plus `$TMUX_PANE`, sends a `proto.Event` to the daemon over a Unix socket (`~/.capataz/capataz.sock`). Must be fast and never block Claude.
- **`internal/daemon`** — one `launchd`-managed background process. Maps events → per-session state, polls plan usage centrally, prunes dead-pane sessions (5s sweep), and broadcasts `proto.Push` to every connected dashboard. Persists sessions + event history to SQLite (`~/.capataz/history.db`, `internal/store`).
- **`internal/tui`** — the Bubble Tea dashboard. Two launch modes: **popup** switcher (`CAPATAZ_POPUP=1`, transient — selecting quits) and **sidebar** (a normal pane — selecting switches without quitting). Several can run at once; all subscribe to the same daemon state. Acts on sessions purely through tmux (`tmux.go`: jump/reply/kill-window/rename/new-session/attach).
- **`internal/proto`** — wire types shared by all three (`Event`, `Session`, `Push`, `Usage`, state enum).
- **`internal/issues`** — reads the per-repo markdown backlog (`.scratch/<feature>/issues/NN-slug.md`) for the issue view. Pure Go, no daemon involvement: `Scan(repoRoot)` parses the `Status:`/`Type:`/`Blocked by:`/`Touches:` header lines; `SetStatus` rewrites the `Status:` line; `Bucket` collapses status to todo/doing/blocked/done; `ParallelSafe` derives waves of ready issues with pairwise-disjoint `Touches` and no blocking edge (`PathsOverlap` does the path-segment-prefix comparison; empty `Touches` ⇒ overlaps everything). Also hosts the `capataz issue` CLI (`Run`: claim/resolve/status/list/new) — `new` serializes numbering per feature (`lockFeature`). Files are the source of truth — the TUI re-reads them, no daemon state.
- **`internal/worktree`** — thin `git worktree` helper (`Add`/`Remove`/`List`) plus `PrimaryCheckout(dir)` (the main working tree for any path inside a repo/worktree, via `git worktree list --porcelain`). Used so the `.scratch` backlog always resolves to the primary checkout even when a session runs in a linked worktree: `capataz issue new` and the TUI's backlog scan both route through it (behavior-preserving — a normal checkout is its own primary; the TUI resolves it only on cwd-change, never per tick). The dispatch side that actually *creates* worktrees is future work (`parallel-workflow` epic).
- Support: **`internal/paths`** (on-disk locations + `dev.capataz` launchd label), **`internal/config`** (`~/.config/capataz/config.json`), **`internal/install`** (hook wiring + launchd plist + skills copy), **`internal/logging`**.

Issue tracking is **TUI-read + a file-writing CLI + shipped skills** — no daemon, no proto types. The backlog is scoped to the selected session's repo. Two surfaces, split by role: the **sidebar** shows a concise read-only tracker (colored status dots + counts, sorted attention-first via `issues.Bucket`), rendered **only when `!m.popup`**; the **popup** (`prefix+g`) is the interaction surface where `i` opens the full-screen start-work view. Both read from a **cache** (`m.issueList`) refreshed off the render path — `syncSelectedIssues` runs on the tick and on selection change; `View()` never scans the filesystem. `issues.Bucket` is the single collapse from the open-ended `Status` vocabulary to `todo`/`doing`/`blocked`/`done`. Skills live in `skills/` (repo root), embedded via `//go:embed` in `embed_skills.go` and copied to `~/.claude/skills` by `capataz install`.

Shipped skills (`skills/`, embedded + installed): **`capataz-splitter`** (plan → vertical-slice issues with predicted `Touches:`; replaces `/to-issues`), **`capataz-work`** (claim → implement → optional sanity-check → resolve), **`capataz-review`** (fresh-context review → files findings as issues). The **`Touches:`** header + `issues.ParallelSafe` power an advisory "parallel-safe" hint in the issue view; real parallel *dispatch* is gated on worktree isolation (see `.scratch` epic `parallel-workflow`), so today it's display-only.

The **review loop** rests on the context distinction: a per-issue **sanity check** (opt-in `review.sanity_check`) runs as a **subagent** inside the implementer's session (bounded subtask, result returns to the implementer) — `issuePrompt` appends the request, the `capataz-work` skill runs it before resolving. The full review (`R` in the issues view) runs in a **fresh session** (`startReview` → new session running `capataz-review`) precisely so it can't inherit the implementer's assumptions; it reviews the working/branch diff and files findings back as new issues via `capataz issue new`. Subagent vs. new-session here is deliberate: a subagent's result must return to a caller (sanity check), a fresh review must *not* share context (full review). Never dispatch a whole issue to a subagent — subagents share `session_id` and can't be tracked as their own row.

## Gotchas (read before changing things)

- **Tracks only tmux sessions.** The daemon ignores a new session with no tmux pane, and the sweep drops sessions whose pane died. Task **subagents share their parent's `session_id`** (they're in-session sidechains) — so they can't be separate rows; the dashboard shows a `⊂N` count on the parent. The subagent-spawn tool is named **`Agent`** in current Claude Code (older: `Task`) — match both.
- **Popups have no `$TMUX_PANE`.** Anything that must work from the popup (e.g. new-session) must not depend on `TMUX_PANE` — gate on `$TMUX`. For client dimensions in a popup use `clientSize()` (`tmux list-clients`), not `display-message -p` (returns empty there).
- **tmux resizes panes proportionally.** The sidebar self-pins to its config width on a window resize (`pinSidebar`); new sessions are created at the client's real size so the split isn't 50/50.
- **Render must never panic.** Negative width/height math (slices, `strings.Repeat`) crashes the TUI, which closes its tmux pane (looks like "the sidebar disappeared"). Guard them; `TestRenderNoPanic` is the safety net.
- **Killing a session** sends `/quit` (graceful, fires `SessionEnd`) then `kill-window` after a grace period (tears down the sidebar too). If you're killing the session you're attached to, it hops the client to another managed session first.
- **launchd PATH**: the plist bakes the user's PATH (+ Homebrew) so the daemon can run `tmux`. Re-run `./capataz install` after PATH-relevant changes.
- **Usage gauge is unofficial.** The daemon reads the Claude Code OAuth token from the Keychain and calls `api/oauth/usage` (the endpoint `/usage` uses). URL + keychain item + interval + on/off are all in `config`; never hardcode them, and keep the "may change / disable-able" framing.
- **No external image/CLI deps** beyond the Go modules in `go.mod` (Bubble Tea, Lipgloss, modernc sqlite). Keep it dependency-light.
- **Issue file format is mattpocock-compatible, on purpose.** `.scratch/<feature>/issues/NN-slug.md` with `Status:`/`Type:`/`Blocked by:` header lines — same layout `/to-issues`, `/triage`, `/wayfinder` produce, so those planning skills interoperate. `Status` is free-form (triage roles *and* `claimed`/`resolved`); the parser (`internal/issues`) is deliberately tolerant and ignores unknown lines. Don't tighten it into a strict enum. `SetStatus` only touches the `Status:` line above the first `## ` section, so a "Status:" inside a `## Comments` thread is safe.
- **Starting an issue = one `claude "<prompt>"`.** The new-session path passes the opening prompt as claude's positional arg (via `newClaudeSession`'s `prompt`), avoiding send-keys boot-timing races; hand-to-selected uses `reply()`. If a claude build ever drops the positional-prompt behavior, only the new-session branch is affected.
- **Adding/removing a shipped skill** needs a rebuild (the `//go:embed skills` tree is baked into the binary) **and** `./capataz install` to re-copy into `~/.claude/skills`.
