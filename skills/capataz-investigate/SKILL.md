---
name: capataz-investigate
description: Diagnose a bug down to its root cause and file (or annotate) a ready-for-agent fix issue in the local .scratch backlog — it diagnoses, it does not fix. Invoked when the user says "investigate this bug", "diagnose <symptom>", "find the root cause", or hands you a failing test / crash / misbehavior to chase down.
---

# capataz-investigate

Chase a bug to its **root cause** and hand off a fix — you diagnose, you do **not** fix. This is the diagnosis half of capataz's separate-concerns split: just as `capataz-review` files findings for `capataz-work` to implement rather than editing the code itself, you produce a *diagnosis* and file (or annotate) a `ready-for-agent` issue carrying the repro + root cause, and a later `capataz-work` agent implements the fix in fresh context. Resist the pull to "just fix it while I'm here" — a fix written by the context that guessed the cause is the fix least likely to be checked against the actual defect. Natural fit for `Type: research`.

You were given a **symptom** (a failing test, a crash, a wrong output, a misbehavior) and usually the **feature slug** the fix issue should be filed under. If the feature slug is unclear, ask or infer it from where the buggy code lives.

## Process

### 1. Build a red feedback loop

Find (or write) **one command that reproduces the bug** and fails because of it — a single failing test, a script, a one-liner. This is your red light: it must be red *for the reason you're chasing*, run fast, and turn green only when the bug is actually gone. Everything downstream leans on it, so get it genuinely red before going further — a repro that fails for an unrelated reason will send you chasing a ghost. If you can't reproduce it at all, that itself is the finding: stop and report what you tried and what you'd need (see step 6).

### 2. Reproduce and minimize

With the loop red, strip the repro down to the smallest input, shortest path, and fewest moving parts that still trigger it. Remove unrelated setup, shrink the data, cut the steps — each thing you can delete while it stays red is a thing that *isn't* the cause. A minimal repro is both your sharpest diagnostic tool and the most valuable thing you'll hand the fixer.

### 3. Form 3–5 falsifiable hypotheses

Write down **3 to 5 concrete, falsifiable hypotheses** for the root cause — ranked by likelihood. Each must be a specific claim you can *prove wrong* with an observation, not a vague area ("something in the parser"). Good: "the off-by-one is in `Bucket`'s boundary check because it uses `<` where it should use `<=`." Ground them in the actual code — read the paths the repro exercises before guessing. Ranking them focuses your instrumentation on the likeliest cause first.

### 4. Instrument one variable at a time

Test hypotheses by **changing one variable at a time** — one log line, one assertion, one breakpoint, one input tweaked — and observe. Never change two things at once; if you do and the symptom moves, you can't tell which one mattered. Each observation should confirm or kill a hypothesis. Let the evidence, not your first hunch, drive which hypothesis survives. Keep going until exactly one stands — the survivor, confirmed against the code, is your root cause. If all 3–5 die, form the next batch from what you learned and repeat.

### 5. Confirm the root cause

You have the root cause when you can point to the **specific defect** (file:line, the wrong condition/value/assumption) *and* explain the full causal chain from that defect to the observed symptom, consistent with every observation from step 4. Sanity-check it: if the story requires hand-waving over a step, you're not there yet — keep instrumenting. Don't file a "probably" as a root cause.

### 6. File (or annotate) a ready-for-agent fix issue

Hand off the fix as a backlog issue — **do not edit the buggy code.**

If the symptom **already has an issue** (you were dispatched from one, or one exists for it), annotate that issue instead of filing a duplicate:

```sh
capataz issue comment .scratch/<feature>/issues/NN-<slug>.md \
  "Root cause: <defect at file:line> → <causal chain to symptom>. Repro: <the red command>. Fix: <what 'fixed' looks like>."
```

Otherwise file a new one:

```sh
capataz issue new <feature> "Fix: <concise symptom → cause>" \
  --body "Symptom: <what's wrong>. Repro: <the minimal red command that reproduces it>. Root cause: <the specific defect at file:line and the causal chain to the symptom>. Fix: <what 'fixed' looks like>. Done when: the repro command goes green."
```

The issue must carry, at minimum: the **repro** (the red command), the **root cause** (defect + causal chain, cited to file:line), and what **fixed** looks like — enough that a fresh `capataz-work` agent can implement and verify the fix without redoing your investigation. Leave `Status: ready-for-agent` so it's dispatchable. If you never reproduced or never confirmed a single root cause, say so plainly rather than filing a guess — file the issue as `needs-info` (`capataz issue status … needs-info`) with what you found and what's still unknown.

### 7. Report

Print a short summary: the repro command, the confirmed root cause (defect + one-line causal chain), and the issue you filed or annotated (its number + title). The fix issue appears in the dashboard tracker as `todo` for a `capataz-work` agent to pick up.
