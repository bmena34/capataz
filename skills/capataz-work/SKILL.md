---
name: capataz-work
description: Work a single issue from the local .scratch/ backlog end to end — claim it, implement the vertical slice, then resolve it. Invoked when capataz (or you) dispatches an agent onto a specific issue file. Also use when the user says "work on issue <path>", "pick up <NN-slug>", or "grab the next ready issue".
---

# capataz-work

Take one issue from this repo's local markdown backlog and carry it from `ready-for-agent` to `resolved`. capataz dispatches agents onto issues by asking you to run this skill with an issue path; you can also run it by hand.

## The backlog format

Issues live as markdown files in `.scratch/`, one feature per directory:

- Feature directory: `.scratch/<feature-slug>/`
- Issue file: `.scratch/<feature-slug>/issues/NN-<slug>.md`, numbered from `01`
- Near the top of each file, header lines record state:
  - `Status:` — triage state (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`) or work state (`claimed`, `resolved`)
  - `Type:` — `research` | `prototype` | `grilling` | `task` (may be absent)
  - `Blocked by: NN, NN` — issue numbers in the same feature that must be `resolved` first
- The body describes what to build and its acceptance criteria; `## Answer` and `## Comments` sections are appended at the bottom.

The file is the source of truth. Change `Status:` **only** through `capataz issue …` so the backlog viewer and any other agents stay consistent. This skill uses `capataz issue claim` / `resolve` / `status`; run `capataz issue` with no args to see the full subcommand list.

## Process

### 1. Resolve the target

- If you were given an issue path or `NN-slug`, use it.
- If asked for "the next ready issue", scan `.scratch/*/issues/*.md` and pick the first (lowest number) that is `Status: ready-for-agent`, not `claimed`, and whose `Blocked by` issues are all `resolved`. Run `capataz issue list .` for a quick overview.

Read the full issue file — body, acceptance criteria, and any `## Comments`.

### 2. Check it is actually workable

- If `Status` is already `claimed` or `resolved`, stop and tell the user — another agent may be on it.
- If any `Blocked by` issue is not yet `resolved`, stop and report which blocker is open. Do not start blocked work.
- If the issue is `needs-info` / `needs-triage` (not yet specified), don't implement — tell the user it needs triage first (see `/triage`).

### 3. Claim it

```sh
capataz issue claim .scratch/<feature>/issues/NN-<slug>.md
```

Do this **before** any code changes so the backlog shows the issue is taken and concurrent agents skip it.

### 4. Implement the vertical slice

Build the end-to-end behavior the issue describes — a thin slice through every layer, not one layer in isolation. Respect the acceptance criteria as your definition of done. Follow the repo's existing conventions and any ADRs in the area you touch. Run the project's build/tests before considering it done.

If you get blocked, discover the issue is underspecified, or the acceptance criteria are wrong, **stop and leave the claim in place** — record what you found in a comment and ask the user:

```sh
capataz issue status .scratch/<feature>/issues/NN-<slug>.md needs-info
```

### 4b. Sanity check (only if asked)

If the dispatch prompt (or the user) asks for a sanity check, do this **before resolving**. Spawn a **subagent** (the Agent/Task tool) with a fresh context and give it your `git diff` plus the issue's acceptance criteria. Its job is a quick sanity check, **not** a deep review:

- Does the change actually implement what the issue describes, and satisfy each acceptance criterion?
- Any obvious breakage — references to files/symbols that don't exist, a claimed-passing test that wasn't run, a criterion silently skipped?
- Nothing that looks hallucinated or half-done.

The subagent returns a short verdict + any gaps. Address real gaps, then resolve. This is a guard against fooling yourself — keep it light; the in-depth review is a separate fresh-context pass (`capataz-review`).

**If you are yourself a subagent**, you can't spawn another one — skip this step and tell whoever dispatched you that the sanity check is still owed.

### 4d. Docs pass (any change larger than a single small change)

If your diff is more than a single small change — it touches multiple files, or changes behavior that docs describe — run the **`/capataz-comment-rot`** skill **inline** (follow its steps yourself; don't spawn a subagent, so this works in delegated dispatch too) before resolving. It scrubs the README, CLAUDE.md, and code comments your change touched for rot, and its own rules govern the pass — including that large README/CLAUDE.md changes need the user's OK first. A one-file, comment-true tweak can skip this.

### 4c. Isolated-worktree mode (only if the dispatch says so)

If the dispatch prompt tells you that you are running in an **isolated git worktree** on a branch (`capataz drain` dispatches issues this way, on the branch named in the dispatch prompt), three things change:

- **Commit when you resolve.** Do your work as normal, but as part of resolving, commit it on that branch:

  ```sh
  git add -A && git commit -m "<feature>/NN: <what you did>"
  ```

  Do **not** merge and do **not** switch branches — the drain merges the branch back into the base, and a `capataz-fix-conflict` agent handles any conflict.
- **Ask the operator via the CLI, never in chat.** Nobody reads the drain's chat. When you're blocked on a decision only the operator can make, run

  ```sh
  capataz issue ask <abs issue path> "<question, with the options you see>"
  ```

  keep working on anything that doesn't depend on the answer, then end your turn. The drain notifies the operator and delivers the answer to this same session as a prompt — continue the issue from there. You may ask more than once. `needs-info` (step 4) stays for "this issue is underspecified, stop".
- **The issue path in the prompt is absolute.** It points at the primary checkout's `.scratch/` (which is gitignored and therefore absent in the worktree). Use that absolute path verbatim for `capataz issue claim/resolve` — never a relative one.

In the normal (non-worktree) case, behavior is unchanged: don't commit unless the user asks; just surface the diff.

Either way: **Never commit `.scratch/`.** It's the local-only backlog, ignored by the global git excludes file `capataz install` sets up. Never `git add -f` it, and never add `.scratch` to a repo's `.gitignore` — that line would get pushed.

### 4e. Terraform race check (only if the diff touches Terraform)

Before resolving, check whether your change touches Terraform:

```sh
capataz tf check
```

It diffs this branch against the base, `origin/main` (else `main`) by default. If your branch was cut from another branch (a drain on a feature branch cuts worktrees from it), pass `--base <that-branch>`, or the base's own unmerged changes show up as yours. It prints `no Terraform changes vs <base>` when no `*.tf`, `*.tfvars` or `.terraform.lock.hcl` file changed — then skip the rest of this step. Otherwise it lists the affected root modules and any findings (open PRs or other worktrees touching the same roots, roots the base changed since you branched, files mapping to no known root). Exit code: `0` clean, `1` findings, `2` error.

When Terraform did change:

1. **Review in a fresh context.** Spawn a subagent (the Agent/Task tool) to run the **`/capataz-terraform`** skill on this worktree, passing the feature slug (the `<feature>` of your issue path) and the roots from `capataz tf check`. It files each finding as a `needs-triage` issue and ends with a `clean` or `N findings` verdict line. **If you are yourself a subagent**, you can't spawn one — skip it and say the Terraform review is still owed.
2. **Any finding from either goes to the operator.** In isolated-worktree mode (4c), ask via the CLI, listing every finding (the tf check messages with their PR URLs, and the paths of the issues the review filed) and the options:

   ```sh
   capataz issue ask <abs issue path> "Terraform race findings: <list>. Options: proceed (resolve as is) / wait (hold until <PR or branch> lands) / rework (change the Terraform first)."
   ```

   then end your turn. Interactively, ask the same in chat. Act on the answer before resolving: proceed → step 5; wait → commit your work (worktree mode), `capataz issue comment` what it waits on, set it `needs-info` and stop (the drain keeps the worktree and branch); rework → change the code and re-run this step.

Never `terraform apply` as part of this check.

### 5. Resolve it

When the acceptance criteria are met and checks pass:

```sh
capataz issue resolve .scratch/<feature>/issues/NN-<slug>.md \
  --answer "One-paragraph summary of what shipped and where (key files, follow-ups)."
```

This sets `Status: resolved` and appends your summary under an `## Answer` heading. Do not commit or push unless the user asks — surface the diff and let them decide. (Exception: in isolated-worktree mode you **do** commit on the branch — see 4c.)
