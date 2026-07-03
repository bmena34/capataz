---
name: capataz-teach
description: Explain a codebase area grounded in the real files (with file:line citations), then run a short in-terminal quiz whose answers you check against the code. A lean, terminal-native learning tool — no lesson files, no learning-records. Invoked when the user says "teach me <area>", "explain the <package> code", or "quiz me on the codebase".
---

# capataz-teach

Help someone learn a real codebase area by **reading it with them**: explain how a chosen part actually works, grounded in the files on disk and cited with `file:line`, then check their understanding with a short quiz whose answers come from the code — not from memory. This is a deliberately stripped-down take on mattpocock's `teach`: no `MISSION.md`, no HTML lessons, no learning-records — just the terminal, the code, and a quiz. It's an **operator tool**: you run it interactively for a human. There's no dispatch loop, no dashboard interaction, and by default nothing is written to disk.

Everything you say must be **grounded in the actual files**. Never explain from general knowledge or what the code "probably" does — open it, read it, and cite it. If you're unsure, go read the file before you claim anything.

## 1. Pick the area

Figure out **what to teach**:

- If the user named an area ("teach me the daemon", "explain `internal/issues`", "how does dispatch work"), use it.
- If they were vague ("quiz me on the codebase", "teach me something"), look around first — `git ls-files`, read the top-level `README.md` / `CLAUDE.md` / any ADRs — then propose 2–3 concrete areas (a package, a subsystem, a data flow) and let them pick. Don't guess and dive in.

Scope it to something teachable in one sitting — one package, one data flow, one subsystem — not the whole repo. If the ask is too broad, narrow it and say so.

## 2. Read it for real

Before explaining anything, **read the code**:

- Map the area: entry points, the key types/functions, how data moves through it. Use `rg` and read the actual files — follow the calls, don't stop at names.
- Read the surrounding docs (`CLAUDE.md`, package comments, ADRs) so your explanation matches the project's own framing and vocabulary.
- Note the non-obvious bits — the gotchas, the invariants, the "why it's done this way" — because those make the best explanations *and* the best quiz questions.

## 3. Explain, grounded in the files

Walk the user through the area. Structure it so it builds — start with the shape (what this area is responsible for, its main pieces), then how a real path flows through it, then the subtleties.

**Cite as you go.** Every concrete claim points at where it lives, as a clickable `file:line` reference (e.g. "the sweep prunes dead-pane sessions every 5s — `internal/daemon/daemon.go:212`"). The citations are the whole point: they let the learner jump straight to the code and verify you, and they keep you honest. Prefer a citation over a paraphrase; quote a short key line when it makes the point.

Keep it conversational and terminal-native — plain prose and short excerpts, not a generated document. Check in as you go ("want me to go deeper on X, or move on?") rather than dumping everything at once.

## 4. Quiz — and check the answers against the code

End with a **short quiz** (3–5 questions) on what you just covered. Ask them one at a time, wait for each answer, and **verify their answer against the actual code**, not against your own recollection:

- Favor questions with a definite, code-checkable answer — "what happens to a session whose tmux pane dies?", "which function derives the parallel-safe waves?", "why does dispatch pre-seed the trust dialog?" — over trivia.
- When they answer, re-read the relevant file if needed, then confirm or correct with a `file:line` citation so they can see it for themselves. If they're partly right, say exactly which part and point at the code.
- Keep it encouraging and low-stakes — this is a learning check, not an exam. Adapt difficulty to how they're doing.

After the last question, give a one-paragraph recap of the area and the key takeaways.

## 5. Persistence is optional and off by default

By default, **write nothing** — the session lives in the terminal and that's it.

Only if the user explicitly asks to keep a summary, write a short markdown note to `.scratch/<feature>/notes/` (create the dir if needed) — a plain recap with the `file:line` citations, nothing more. Never create lesson files, learning-records, or any of the heavier `teach` artifacts; that's the machinery this skill deliberately drops.
