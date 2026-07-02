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

The file is the source of truth. Change `Status:` **only** through `capataz issue …` so the dashboard and any other agents stay consistent.

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

Do this **before** any code changes so the dashboard shows the issue is taken and concurrent agents skip it.

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

The subagent returns a short verdict + any gaps. Address real gaps, then resolve. This is a guard against fooling yourself — keep it light; the in-depth review is a separate fresh-context pass (the dashboard's `R`).

### 5. Resolve it

When the acceptance criteria are met and checks pass:

```sh
capataz issue resolve .scratch/<feature>/issues/NN-<slug>.md \
  --answer "One-paragraph summary of what shipped and where (key files, follow-ups)."
```

This sets `Status: resolved` and appends your summary under an `## Answer` heading. Do not commit or push unless the user asks — surface the diff and let them decide.
