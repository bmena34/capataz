# CLAUDE.md

Guidance for working on **capataz** with Claude Code. User-facing docs are in `README.md`; this file is the contributor/agent map: architecture, the dev loop, and the non-obvious gotchas.

## What it is

The workflow layer for Claude Code agents running under **herdr**: a per-repo `.scratch` markdown backlog, parallel-safe dispatch waves, a drain orchestrator, and the shipped skills. Pure Go, one binary with subcommands. The repo root is also the **herdr plugin** (`herdr-plugin.toml`): its panes and actions all run `./bin/capataz`. herdr owns panes, worktrees, agent state and persistence; capataz has no daemon, no hooks, no database. (The old tmux dashboard is gone; the tag `tmux-final` is its last version.)

## Build / test / run

```sh
make build              # build ./capataz (local dev binary)
make install            # build + sudo install to /usr/local/bin (skills call `capataz issue` on PATH)
make vet / make test    # go vet ./... / go test ./...
sh scripts/build.sh     # build ./bin/capataz — the binary the herdr plugin runs
```

**Dev loop:** CLI changes → `make build`. Plugin changes → `sh scripts/build.sh` (popups re-exec the binary on every open). Manifest changes → `herdr server reload-config`. Skill/agent changes → rebuild (they're `//go:embed`ed) **and** `capataz install` to re-copy into `~/.claude`.

## Architecture

`main.go` dispatches subcommands to packages:

- **`internal/issues`** — the backlog. `Scan(repoRoot)` parses `.scratch/<feature>/issues/NN-slug.md` (`Status:`/`Type:`/`Blocked by:`/`Touches:` headers); `SetStatus` rewrites the `Status:` line; `Bucket` collapses status to todo/doing/blocked/done; `ParallelSafe` derives waves of ready issues with pairwise-disjoint `Touches` and no blocking edge (`PathsOverlap` compares path-segment prefixes; empty `Touches` ⇒ overlaps everything); `DispatchBranch` names worktree branches `capataz/<feature>-NN-slug`. Also hosts the `capataz issue` CLI (`Run`: claim/resolve/status/comment/list/new/waves). Files are the source of truth.
- **`internal/drain`** — `capataz drain`: loops `capataz issue waves --json` → for each issue in a wave, `herdr worktree create` → `capataz trust-dir` → start a Claude agent with `workPrompt` → `herdr agent wait` → verify the branch has commits → merge back (serialized by `mergeMu`; a conflict starts a `capataz-fix-conflict` agent in the base repo) → re-scan. All external calls go through the `Runner` interface, so tests use a fake.
- **`internal/usageview`** — `capataz usage-view`: the plan gauge (shells out to `capataz usage`) plus per-pane context gauges read straight from Claude Code transcripts, located by the session id in `herdr agent list`.
- **`internal/issueview`** — `capataz issues`: read-only Bubble Tea backlog viewer for the backlog popup.
- **`internal/worktree`** — `PrimaryCheckout(dir)`: the main working tree for any path inside a repo or linked worktree. The backlog always lives in the primary checkout.
- **`internal/install`** — copies the embedded skills (`skills/`) and agent defs (`agents/`) into `~/.claude`, writes the default config, adds `.scratch/` to the global git excludes (`gitignore.go`), and removes tmux-era leftovers (capataz hooks in `settings.json`, the `dev.capataz` launchd plist).
- Root: `usage.go` (`capataz usage`, Keychain + usage endpoint), `trust.go` (`capataz trust-dir`, pre-seeds folder-trust in `~/.claude.json`). Support: `internal/config` (`~/.config/capataz/config.json`: `usage` + `context.limits`), `internal/paths`.

Plugin files: `herdr-plugin.toml`, `scripts/build.sh`, `bin/*.sh` (pane entrypoints and actions). `drain` and `usage-view` shell out to the running executable (`os.Executable`, overridable with `CAPATAZ_BIN`), so the plugin never depends on whatever `capataz` is on PATH.

Shipped skills (`skills/`, embedded): `capataz-start` → `capataz-splitter` (plan → issues with `Touches:`) → `capataz-triage` → `capataz-work` (claim → implement → resolve; commits on its branch only when the prompt says it's in an isolated worktree) / `capataz drain`; `capataz-review` and `capataz-security` (fresh-context reviews that file findings as issues), `capataz-investigate`, `capataz-fix-conflict`, `capataz-comment-rot`, `capataz-teach`.

## Gotchas (read before changing things)

- **The drain's prompt is a contract with `capataz-work`.** The drain fails an issue whose branch has no new commits, and `capataz-work` only commits when told it's in an isolated worktree on a branch. Change `workPrompt` and skill step 4c together.
- **The drain needs herdr's Claude integration** (`herdr integration install claude`); `herdr agent wait` never returns without it.
- **Pane commands resolve against the pane's `--cwd`, not the plugin root.** Use `sh -c 'exec "$HERDR_PLUGIN_ROOT/bin/…"'`. Actions get the focused pane's cwd only via `HERDR_PLUGIN_CONTEXT_JSON` (`focused_pane_cwd`); their own `PWD` is the plugin root.
- **The plugin ID `bmena34.capataz` is user-facing**: herdr keybinds reference `bmena34.capataz.<action>`, and `bin/open-*.sh` pass it to `herdr plugin pane open`.
- **`.scratch/` is ignored globally, not per repo.** Never add it to a repo's `.gitignore` (that line gets pushed), and keep the "never commit `.scratch/`" rule in `capataz-work` and `capataz-fix-conflict`. `uninstall` deliberately leaves the exclude in place.
- **Issue file format is mattpocock-compatible, on purpose.** Same layout `/to-issues`, `/triage`, `/wayfinder` produce. `Status` is free-form; the parser is deliberately tolerant and ignores unknown lines — don't tighten it into a strict enum. `SetStatus` only touches the `Status:` line above the first `## ` section.
- **`claim` is a compare-and-set, not a stamp.** It succeeds only from `ready-for-agent` → `claimed` (else `already claimed` / `not claimable: <status>`; `--force` overrides). Read-check-write runs under a per-file O_EXCL lock (`<path>.capataz-claim.lock`) with the status re-read inside it, so racing claims yield one winner. `resolve`/`status` stay unconditional. `issue new` serializes numbering per feature (`lockFeature`).
- **Usage gauge is unofficial.** It reads the Claude Code OAuth token from the Keychain and calls `api/oauth/usage`. URL, keychain item and on/off live in `config`; never hardcode them, and keep the "may change / disable-able" framing.
- **Context windows can't be auto-detected for 1M sessions** — transcripts carry the bare model id. `context.limits` / `CAPATAZ_CONTEXT_LIMIT` pin them; a session measured above its assumed window widens instead of showing >100%.
- **Keep it dependency-light**: Bubble Tea + Lipgloss only.
