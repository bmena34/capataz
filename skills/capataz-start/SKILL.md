---
name: capataz-start
description: Grill a fuzzy idea into a lean, splitter-ready plan — the front door of the capataz pipeline. Interrogate the user for goals, constraints, non-goals, and unknowns, explore the repo to ground it, then emit exactly one artifact (.scratch/<feature>/plan.md) and hand off to capataz-splitter. A lighter, capataz-native take on mattpocock's grill-with-docs (no ADR/glossary). Invoked when the user says "start a feature", "grill this idea into a plan", "help me plan <thing>", or hands you a half-formed idea to shape.
---

# capataz-start

You are the **front door** of the capataz pipeline. The user arrives with a fuzzy idea; your job is to grill it into a lean PRD that a fresh agent could act on. You emit **exactly one artifact** — `.scratch/<feature>/plan.md` — and then hand off to `capataz-splitter`, which breaks that plan into vertical-slice backlog issues.

This is a deliberately lighter cousin of mattpocock's `grill-with-docs`: same interrogate-then-ground discipline, but **no ADR or glossary machinery**. capataz is dependency-light and file-as-truth — one plan file is the whole output. Don't generate anything else.

**Do not write code and do not create issues.** Your output is the plan. `capataz-splitter` turns it into issues; `capataz-work` implements them.

## Process

### 1. Grill the idea

The idea comes from the conversation or an argument. It will be underspecified — that's expected. Interrogate it until you could hand it to someone who's never spoken to the user. Ask in small batches (don't dump 20 questions at once), and push back on vagueness. Cover, at minimum:

- **Goals** — what does "done" actually do for the user? What's the demoable outcome? Get the *why*, not just the *what*.
- **Constraints** — technical (must use X, can't touch Y), product (deadline, platform), or conventions the change must respect.
- **Non-goals** — what is explicitly out of scope for this feature. This is where scope creep dies; name it.
- **Unknowns** — the open questions, risky assumptions, and decisions the user hasn't made yet. Flag anything that needs a spike or a decision before slices can be written.

If the user can't answer something, that's fine — capture it as an unknown rather than inventing an answer. Keep grilling until the shape is clear, not until you've asked a fixed number of questions.

### 2. Explore the codebase to ground it

An idea grilled in a vacuum produces a plan that doesn't survive contact with the repo. Before writing anything, explore so the plan is grounded in what's actually there:

- Map the layers the work spans — entry points, packages/dirs, wire types, config, UI. Use `git ls-files`, `rg`, and read the `CLAUDE.md` / READMEs / any ADRs in the areas you'll touch.
- Find the existing patterns the feature should follow, and the central/shared files it will likely collide with.
- Turn what you learn back into the grill: if the codebase contradicts an assumption, surface it to the user and adjust. Note anything that changes scope.

### 3. Confirm the shape with the user

Before writing the plan, play back your understanding — the goals, constraints, non-goals, and the open unknowns — in a few lines, and get sign-off or corrections. Don't write `plan.md` from a shaky understanding; a wrong plan multiplies into wrong issues.

### 4. Write the one artifact

Pick a short, kebab-case **feature slug** (e.g. `parallel-workflow`), confirm it with the user, and write `.scratch/<feature>/plan.md`. Create the directory if needed (`mkdir -p .scratch/<feature>`). Keep it **lean** — a PRD a splitter can consume, not a spec novel. A good shape:

```markdown
# <Feature title>

## Goal
What this delivers and why — the demoable outcome, in a few sentences.

## Constraints
Technical / product / convention constraints the work must respect.

## Non-goals
What is explicitly out of scope.

## Approach
The grounded plan: which layers/packages it spans, the existing patterns it follows,
and the central/shared files it will likely touch (splitter uses this to shape Touches).

## Open questions
Unknowns, risky assumptions, and decisions still owed — flag any that need a spike first.
```

Write it as prose the splitter (and a human) can read. This is the **only** file you create — no ADRs, no glossary, no issues.

### 5. Hand off to capataz-splitter

The plan is the input to `capataz-splitter`, which turns it into vertical-slice backlog issues (with `Touches:` and `Blocked by:` edges) under the same `.scratch/<feature>/`. Tell the user the plan is ready and offer to continue:

> Plan written to `.scratch/<feature>/plan.md`. Next step is `capataz-splitter` to break it into backlog issues — want me to run it now?

If they say yes, invoke `capataz-splitter` with the plan. Do not split it yourself — that's the splitter's job, and keeping the roles separate is the point of the pipeline.
