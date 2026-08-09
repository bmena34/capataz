# capataz-herdr — testing runbook

A staged plan to validate the herdr port end to end, ordered **safe → risky**.
Do the stages in order; each has an **Expected** and a **Record** line. Stop and
note anything that deviates — Stage 5 (conflict path) is expected to surface bugs
(that's issue 07).

Two binaries are involved:
- **herdr** — the substrate (panes, sessions, agent state). ~/opt/homebrew.
- **capataz** — the workflow engine (`issues`, `issue waves`, `usage`, skills).
  Currently on branch `herdr-port`; must be built + on PATH.

---

## Stage 0 — Install / prerequisites

### 0.1 Put the branch build of capataz on PATH
```sh
cd ~/code/capataz
git switch herdr-port
make install                 # sudo installs ./capataz → /usr/local/bin
capataz version
capataz issues --help        # new
capataz issue waves --help   # new (prints usage error listing subcommands)
capataz usage                # new — should print a Session/Weekly gauge
```
**Expected:** all three new surfaces resolve; `capataz usage` prints a gauge.
Once this is done you can drop the `CAPATAZ_BIN=…` override used during dev.

### 0.2 Skills + agents (herdr-only install)
```sh
capataz install --herdr              # skills + agents + config, NO hooks/daemon
ls ~/.claude/skills | grep capataz   # capataz-work, capataz-fix-conflict, …
```
The dispatched agents run `/capataz-work` and `/capataz-fix-conflict`, so these
must exist. `capataz install --herdr` lays down only what the herdr flow needs
(skills, per-role agents, default config) and skips capataz's own hook wiring +
launchd daemon. (Plain `capataz install` also works but additionally wires the
tmux/launchd machinery, which just idles under herdr.)

### 0.3 Install herdr's Claude state integration  ← REQUIRED, currently missing
```sh
herdr integration status | grep claude     # shows "not installed"
herdr integration install claude           # adds ~/.claude/hooks/herdr-agent-state.sh
herdr integration status | grep claude     # now "installed"
```
**Why it matters:** this hook is how herdr detects a Claude agent's
working/blocked/done state. `herdr agent wait --until done` (the drain's
completion signal) depends on it. **Without this the drain will hang.**
Note: this coexists with capataz's own Claude hooks; both fire, no conflict.

### 0.4 Build + link the plugin
```sh
cd ~/code/capataz-herdr
sh scripts/build.sh                        # produces ./bin/drain
herdr plugin link ~/code/capataz-herdr
herdr server reload-config
herdr plugin list                          # shows bmena34.capataz-herdr enabled
```

### 0.5 Retire the spike plugin (recommended)
The old spike `capataz.workflow` is still linked and also defines an `issues`
pane/action — two plugins with the same action name is confusing. Unlink it:
```sh
herdr plugin unlink capataz.workflow
```

### 0.6 (Optional) keybindings
In `~/.config/herdr/config.toml`, bind the actions (`type = "plugin_action"`,
`plugin = "bmena34.capataz-herdr"`, `action = "issues" | "drain" | "usage"`),
then `herdr server reload-config`. Not required — you can invoke by hand below.

---

## Stage 1 — Backlog viewer (read-only, safe)

From a **pane inside herdr**, cd into a repo that has a `.scratch` backlog
(capataz itself works):
```sh
herdr plugin action invoke issues
```
**Expected:** an overlay lists that repo's issues; `j`/`k` move, `g`/`G`
jump, `q` closes. Feature grouping + status badges render.
**Record:** does it open in the right repo? navigation/quit feel right?

---

## Stage 2 — Usage + context popup (read-only, safe)

```sh
herdr plugin action invoke usage      # overlay
# or, standalone (no herdr pane needed):
./bin/usage-view -once
capataz usage                         # just the plan line
```
**Expected:** the plan gauge (Session / Weekly bars + reset times, or a plan
line for Enterprise/API accounts), then a `Context` table with one row per herdr
agent pane, and a `q quit · refresh 2s` footer.

Check each:
- [ ] one row per pane in `herdr agent list`; `▸` marks the pane you opened it from
- [ ] context numbers plausible vs. that agent's own `/context`
- [ ] the gauge climbs when you leave it open and the agent keeps working (2s refresh)
- [ ] `q` / `Esc` closes it, and the pane's shell is usable afterwards (terminal mode restored, cursor visible)
- [ ] rows don't shuffle between refreshes as agents change status

**1M-context sessions:** transcripts record a bare `claude-opus-5` with no `[1m]`
marker, so the view assumes 200k. If a row reads ~5x too full, pin the window:
```sh
./bin/usage-view -once -limit 1m      # confirm the number is right, then persist it:
# ~/.config/capataz/config.json → { "context": { "limits": { "claude-opus-5": "1m" } } }
```

**Degradation checks (all should stay useful, none should crash):**
```sh
CAPATAZ_BIN=/nonexistent ./bin/usage-view -once   # dim plan note; context rows still render
HERDR_BIN=/nonexistent   ./bin/usage-view -once   # red error line in place of the table
COLUMNS=34 ./bin/usage-view -once                 # bars drop rather than wrapping
```

**Record:** numbers plausible vs. `claude`'s /usage and /context? formatting OK
at the popup's size?

---

## Stage 3 — Build a throwaway test backlog

The drain needs a real git repo with `ready-for-agent` issues. Make a scratch one:
```sh
mkdir -p ~/tmp/drain-test && cd ~/tmp/drain-test
git init -q && git commit -q --allow-empty -m "init"

# two trivial, NON-overlapping issues (different files → one parallel wave)
capataz issue new drain-demo "Add greeting file" \
  --body "Create greeting.txt containing exactly: hello from drain"
capataz issue new drain-demo "Add farewell file" \
  --body "Create farewell.txt containing exactly: bye from drain"
```
Then edit each `.scratch/drain-demo/issues/0N-*.md` to add a `Touches:` line so
they're parallel-safe and scoped:
```
Touches: greeting.txt        # issue 01
Touches: farewell.txt        # issue 02
```
Verify the plan:
```sh
capataz issue waves drain-demo --json
```
**Expected:** one wave containing both issues (disjoint Touches).

---

## Stage 4 — Dry-run drain (no side effects)

```sh
cd ~/code/capataz-herdr
./bin/drain --dry-run --repo ~/tmp/drain-test drain-demo
```
**Expected:** prints the planned per-issue command sequence (claim → worktree
create → agent start → agent wait → resolve → merge → worktree remove) for both
issues. Nothing is created.
**Record:** do the branches/commands look right?

---

## Stage 5 — Live drain, CLEAN path (the real test)

Run it for real, one at a time first, watching herdr's sidebar:
```sh
./bin/drain --repo ~/tmp/drain-test --concurrency 1 --skip-perms drain-demo
```
`--skip-perms` makes it fully hands-off (agents run
`--dangerously-skip-permissions`). Drop it to see the default posture, where a
permission prompt surfaces as a **blocked** agent you attend to.

**Watch for, in order, per issue:**
1. a worktree/workspace appears in herdr (branch `capataz/drain-demo-0N-…`)
2. a `claude` agent starts in its pane and goes **working**
3. it creates the file, commits on the branch, goes **done**
4. drain merges the branch into `~/tmp/drain-test` and removes the worktree
5. `capataz issue waves drain-demo --json` eventually returns empty → "done"

```sh
# after it finishes:
cd ~/tmp/drain-test
git log --oneline           # merge commits for both issues
ls greeting.txt farewell.txt
capataz issue list | grep -A3 drain-demo   # both resolved
```
**Expected:** both files present, both branches merged, both issues resolved.
**Record (the big one):** did `agent wait --until done` fire, or did it hang?
did the merge land? any step need manual nudging? Then try again **without
`--concurrency 1`** to exercise a real parallel wave.

---

## Stage 6 — Live drain, CONFLICT path (validated)

To force a merge conflict you need two issues that both edit the **same** file
but are treated as **parallel-safe** (so they're dispatched in one wave from the
same base). Give them **disjoint (deliberately-wrong) `Touches`** — that's the
real-world trigger: a mis-declared `Touches` where two "parallel-safe" issues
actually collide.

```sh
mkdir -p ~/tmp/conflict-drain && cd ~/tmp/conflict-drain
git init -q && printf '1\n' > VERSION && git add VERSION && git commit -q -m "seed VERSION=1"
capataz issue new conflict-demo "Set VERSION to 2" \
  --body "Overwrite VERSION so it contains exactly one line: 2"
capataz issue new conflict-demo "Set VERSION to 3" \
  --body "Overwrite VERSION so it contains exactly one line: 3"
# edit each issue: give 01 'Touches: alpha.txt' and 02 'Touches: beta.txt'
# (disjoint → one wave; both actually edit VERSION → conflict on merge-back)
capataz issue waves conflict-demo --json   # confirm: one wave of 2
```
Run with concurrency 2 so both dispatch from the same base:
```sh
cd ~/code/capataz-herdr
./bin/drain --repo ~/tmp/conflict-drain --concurrency 2 conflict-demo
```
**Expected (validated):** first branch merges clean; the second **conflicts** →
drain opens the base repo as a herdr workspace, starts a `capataz-fix-conflict`
agent there, waits, **verifies the merge actually completed** (no `MERGE_HEAD`,
no markers), then removes the worktree and closes the base workspace.

**Verify:**
```sh
cd ~/tmp/conflict-drain
git status                 # clean, no MERGE_HEAD
cat VERSION                # no conflict markers
git log --oneline          # a merge commit for EACH branch
capataz issue list         # both resolved
git branch                 # just master (merged branches cleaned up)
herdr pane list            # no leftover base workspaces
```

---

## Teardown

```sh
# remove test worktrees herdr created (if any linger)
herdr worktree list
# unlink the plugin if you want a clean herdr
herdr plugin unlink bmena34.capataz-herdr
# nuke the scratch repo
rm -rf ~/tmp/drain-test
```

---

## Feedback template

Copy this back with notes per stage:

```
Stage 0 install : PASS / FAIL — notes:
Stage 1 viewer  : PASS / FAIL — notes:
Stage 2 usage   : PASS / FAIL — notes:
Stage 3 backlog : PASS / FAIL — notes:
Stage 4 dry-run : PASS / FAIL — notes:
Stage 5 clean   : PASS / FAIL — notes: (did wait fire? merge land?)
Stage 6 conflict: PASS / FAIL — notes: (exact failure + drain log)
Overall feel / rough edges:
```
